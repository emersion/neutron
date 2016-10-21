package imap

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/mail"
	"time"
	"log"

	"github.com/emersion/go-imap"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/neutron/backend/memory"
	"github.com/emersion/neutron/backend/util/textproto"
)

type updatableAttachments interface {
	backend.AttachmentsBackend
	UpdateAttachmentMessage(user, id, msgId string) error
}

type Messages struct {
	*conns
	tmpAtts updatableAttachments
}

func (b *Messages) GetMessage(user, id string) (msg *backend.Message, err error) {
	mailbox, uid, err := parseMessageId(id)
	if err != nil {
		return
	}

	err = b.selectMailbox(user, mailbox)
	if err != nil {
		return
	}

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	seqset, _ := imap.NewSeqSet("")
	seqset.AddNum(uid)
	items := []string{imap.FlagsMsgAttr, imap.SizeMsgAttr, imap.BodyStructureMsgAttr, "RFC822.HEADER"}

	// Get message metadata

	ch := make(chan *imap.Message, 1)
	if err = c.UidFetch(seqset, items, ch); err != nil {
		return
	}

	msgInfo := <-ch
	if msgInfo == nil {
		err = errors.New("No such message")
		return
	}

	//structure := parseBodyStructure(imap.AsList(msgInfo.Attrs["BODYSTRUCTURE"]))

	m, err := mail.ReadMessage(msgInfo.GetBodyPart("RFC822.HEADER"))
	if err != nil {
		return
	}

	msg = &backend.Message{}
	msg.ID = id
	msg.LabelIDs = []string{getLabelID(c.Mailbox.Name)}
	msg.Header = string(header)
	parseMessageInfo(msg, msgInfo)
	textproto.ParseMessageHeader(msg, &m.Header)
	textproto.ParseMessageStructure(msg, structure)

	// Get message content

	preferred := structure.GetPreferredPart()
	cmd, _, err = wait(c.UIDFetch(seqset, "BODY.PEEK["+preferred.ID+"]"))
	if err != nil {
		return
	}

	rsp = cmd.Data[0]
	msgInfo = rsp.MessageInfo()
	body := imap.AsBytes(msgInfo.Attrs["BODY["+preferred.ID+"]"])

	r := preferred.DecodeContent(bytes.NewReader(body))
	slurp, err := ioutil.ReadAll(r)
	if err != nil {
		return
	}
	msg.Body = string(slurp)

	// Check if some attachments are encrypted

	for _, att := range msg.Attachments {
		if att.MIMEType == "application/pgp" {
			// TODO: get correct key packet length
			// TODO: attachment is assumed to be base64-encoded
			// TODO: make sure this is a normalized msgInfo.Attrs key
			cmd, _, err = wait(c.UIDFetch(seqset, "BODY.PEEK["+att.ID+"]<0.2048>"))
			if err != nil {
				return
			}

			rsp = cmd.Data[0]
			msgInfo = rsp.MessageInfo()
			encryptedKeyPacket := imap.AsBytes(msgInfo.Attrs["BODY["+att.ID+"]<0>"])
			att.KeyPackets = string(encryptedKeyPacket)
		}

		att.ID = formatAttachmentId(mailbox, uid, att.ID)
	}

	tmpAtts, err := b.tmpAtts.ListAttachments(user, id)
	if err == nil {
		msg.Attachments = append(msg.Attachments, tmpAtts...)
	}

	return
}

func reverseMessagesList(msgs []*backend.Message) {
	n := len(msgs)
	for i := 0; i < n/2; i++ {
		msgs[i], msgs[n-i-1] = msgs[n-i-1], msgs[i]
	}
}

func (b *Messages) ListMessages(user string, filter *backend.MessagesFilter) (msgs []*backend.Message, total int, err error) {
	// TODO: find a way to search in all mailboxes when label isn't specified
	if filter.Label == "" {
		filter.Label = backend.InboxLabel
	}

	err = b.selectLabelMailbox(user, filter.Label)
	if err != nil {
		return
	}

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	total = int(c.Mailbox.Messages)

	// No messages to fetch
	if c.Mailbox.Messages == 0 {
		return
	}

	set, _ := imap.NewSeqSet("")
	fetchUid := false
	if filter.Limit > 0 && filter.Page >= 0 {
		from := filter.Limit * filter.Page
		to := filter.Limit * (filter.Page + 1)

		if uint32(to) < c.Mailbox.Messages {
			set.AddRange(c.Mailbox.Messages-uint32(from), c.Mailbox.Messages-uint32(to))
		} else {
			set.Add("1:*")
		}
	} else {
		set.Add("1:*")
	}

	// TODO: support filter.Address, filter.Attachments
	search := []imap.Field{}

	dateFormat := "2-Jan-2006"
	if filter.Begin != 0 {
		search = append(search, "AFTER", time.Unix(filter.Begin, 0).Format(dateFormat))
	}
	if filter.End != 0 {
		search = append(search, "BEFORE", time.Unix(filter.End, 0).Format(dateFormat))
	}

	if filter.From != "" {
		search = append(search, "FROM", imap.Quote(filter.From, true))
	}
	if filter.To != "" {
		search = append(search, "TO", imap.Quote(filter.To, true))
	}

	if filter.Keyword != "" {
		search = append(search, "TEXT", imap.Quote(filter.Keyword, true))
	}

	if len(search) > 0 {
		var cmd *imap.Command
		cmd, _, err = wait(c.UIDSearch(search...))
		if err != nil {
			return
		}

		results := []uint32{}
		for _, res := range cmd.Data {
			results = append(results, res.SearchResults()...)
		}

		if len(results) == 0 {
			return // No result
		}

		set, _ = imap.NewSeqSet("")
		set.AddNum(results...)
		fetchUid = true
	}

	var cmd *imap.Command
	if fetchUid {
		cmd, err = c.UIDFetch(set, "UID", "FLAGS", "RFC822.SIZE", "ENVELOPE")
		log.Println(cmd)
		if err != nil {
			return
		}
	} else {
		cmd, err = c.Fetch(set, "UID", "FLAGS", "RFC822.SIZE", "ENVELOPE")
		if err != nil {
			return
		}
	}

	for cmd.InProgress() {
		c.Recv(-1)

		// Process command data
		for _, rsp := range cmd.Data {
			msgInfo := rsp.MessageInfo()
			envelope := imap.AsList(msgInfo.Attrs["ENVELOPE"])

			msg := &backend.Message{}
			msg.ID = formatMessageId(c.Mailbox.Name, msgInfo.UID)
			msg.LabelIDs = []string{getLabelID(c.Mailbox.Name)}
			parseMessageInfo(msg, msgInfo)
			parseEnvelope(msg, envelope)

			tmpAtts, err := b.tmpAtts.ListAttachments(user, msg.ID)
			if err == nil {
				msg.Attachments = append(msg.Attachments, tmpAtts...)
			}

			msgs = append(msgs, msg)
		}

		cmd.Data = nil
	}

	c.Data = nil

	// Check command completion status
	if _, err = cmd.Result(imap.OK); err != nil {
		return
	}

	reverseMessagesList(msgs)
	return
}

func (b *Messages) CountMessages(user string) (counts []*backend.MessagesCount, err error) {
	mailboxes, err := b.getMailboxes(user)
	if err != nil {
		return
	}

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	for _, mailbox := range mailboxes {
		cmd, _, err := wait(c.Status(mailbox.Name, "MESSAGES", "UNSEEN"))
		if err != nil {
			return nil, err
		}

		mailboxStatus := cmd.Data[0].MailboxStatus()

		counts = append(counts, &backend.MessagesCount{
			LabelID: getLabelID(mailboxStatus.Name),
			Total:   int(mailboxStatus.Messages),
			Unread:  int(mailboxStatus.Unseen),
		})
	}

	return
}

func (b *Messages) insertMessage(user, mailbox string, flags imap.FlagSet, mail []byte) (uid uint32, err error) {
	literal := imap.NewLiteral(mail)

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	_, res, err := wait(c.Append(mailbox, flags, nil, literal))
	if err != nil {
		return
	}

	if imap.AsString(res.Fields[0]) != "APPENDUID" {
		err = errors.New("APPEND didn't returned an UID (this is not supported for now)")
		return
	}

	uid = imap.AsNumber(res.Fields[2])
	return
}

func (b *Messages) InsertMessage(user string, msg *backend.Message) (inserted *backend.Message, err error) {
	mailbox, err := b.getLabelMailbox(user, backend.DraftLabel)
	if err != nil {
		return
	}

	flags := imap.NewFlagSet("\\Seen", "\\Draft")
	mail := textproto.FormatMessage(msg)

	uid, err := b.insertMessage(user, mailbox, flags, []byte(mail))

	inserted = msg
	inserted.ID = formatMessageId(mailbox, uid)
	return
}

func (b *Messages) updateMessageFlags(user string, seqset *imap.SeqSet, flag string, value bool) error {
	item := "+FLAGS"
	if !value {
		item = "-FLAGS"
	}

	fields := imap.Field([]imap.Field{imap.Field(flag)})

	c, unlock, err := b.getConn(user)
	if err != nil {
		return err
	}
	defer unlock()

	_, _, err = wait(c.UIDStore(seqset, item, fields))
	if err != nil {
		return err
	}

	return nil
}

func (b *Messages) deleteMessages(user string, seqset *imap.SeqSet) (err error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	fields := imap.Field([]imap.Field{imap.Field("\\Deleted")})
	_, _, err = wait(c.UIDStore(seqset, "+FLAGS", fields))
	if err != nil {
		return
	}

	_, _, err = wait(c.Expunge(seqset))
	if err != nil {
		return
	}

	return
}

// TODO: only supports moving one single message
func (b *Messages) copyMessages(user string, seqset *imap.SeqSet, mbox string) (uid uint32, err error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	_, res, err := wait(c.UIDCopy(seqset, mbox))
	if err != nil {
		return
	}

	if imap.AsString(res.Fields[0]) != "COPYUID" {
		err = errors.New("COPY didn't returned an UID (this is not supported for now)")
		return
	}

	uid = imap.AsNumber(res.Fields[2])
	return
}

func (b *Messages) moveMessages(user string, seqset *imap.SeqSet, mbox string) (uid uint32, err error) {
	uid, err = b.copyMessages(user, seqset, mbox)
	if err != nil {
		return
	}

	err = b.deleteMessages(user, seqset)
	return
}

func (b *Messages) UpdateMessage(user string, update *backend.MessageUpdate) (msg *backend.Message, err error) {
	// Retrieve message from mailbox
	mailbox, uid, err := parseMessageId(update.Message.ID)
	if err != nil {
		return
	}

	err = b.selectMailbox(user, mailbox)
	if err != nil {
		return
	}

	seqset, _ := imap.NewSeqSet("")
	seqset.AddNum(uid)

	msg, err = b.GetMessage(user, update.Message.ID)
	if err != nil {
		return
	}

	// Apply update to message
	update.Apply(msg)

	if update.IsRead {
		err = b.updateMessageFlags(user, seqset, "\\Seen", (update.Message.IsRead == 1))
		if err != nil {
			return
		}
	}

	if update.Starred {
		err = b.updateMessageFlags(user, seqset, "\\Flagged", (update.Message.Starred == 1))
		if err != nil {
			return
		}
	}

	if update.Type {
		err = b.updateMessageFlags(user, seqset, "\\Draft", (update.Message.Type == backend.DraftType))
		if err != nil {
			return
		}
	}

	// Mark a message as sent
	// Correctly handle temporary attachments
	if update.Type && update.Message.Type == backend.SentType {
		// Retrieve temporary attachments
		tmpAtts, _ := b.tmpAtts.ListAttachments(user, update.Message.ID)

		// Insert temporary attachments to message and send it to the server

		outgoing := &backend.OutgoingMessage{
			Message: msg,
		}

		for _, att := range tmpAtts {
			var d []byte
			att, d, err = b.tmpAtts.ReadAttachment(user, att.ID)

			outgoing.Attachments = append(outgoing.Attachments, &backend.OutgoingAttachment{
				Attachment: att,
				Data: d,
			})
		}

		var mailbox string
		mailbox, err = b.getLabelMailbox(user, backend.SentLabel)
		if err != nil {
			return
		}

		flags := imap.NewFlagSet("\\Seen")
		mail := textproto.FormatOutgoingMessage(outgoing)

		var uid uint32
		uid, err = b.insertMessage(user, mailbox, flags, []byte(mail))
		if err != nil {
			return
		}

		msg.ID = formatMessageId(mailbox, uid)

		// Remove temporary attachments
		for _, att := range tmpAtts {
			b.tmpAtts.DeleteAttachment(user, att.ID)
		}
	} else if update.ToList || update.CCList || update.BCCList || update.Subject || update.AddressID || update.Body || update.Time {
		// If one of those is modified, we have to re-send the whole message to the server

		// The message ID will change
		oldId := msg.ID
		msg.ID = "" // Will be overwritten

		// Insert the updated message
		msg, err = b.InsertMessage(user, msg)
		if err != nil {
			return
		}

		// Delete the old message
		err = b.deleteMessages(user, seqset)
		if err != nil {
			return
		}

		// Update temporary attachments message ID
		tmpAtts, _ := b.tmpAtts.ListAttachments(user, oldId)
		for _, att := range tmpAtts {
			b.tmpAtts.UpdateAttachmentMessage(user, att.ID, msg.ID)
		}
	} else if update.LabelIDs != backend.RemoveLabels && len(update.Message.LabelIDs) == 1 {
		// Move the message from its mailbox to another one
		// TODO: support more scenarios

		msg.LabelIDs = update.Message.LabelIDs
		label := msg.LabelIDs[0]

		var newMailbox string
		newMailbox, err = b.getLabelMailbox(user, label)
		if err != nil {
			return
		}

		var newUid uint32
		newUid, err = b.moveMessages(user, seqset, newMailbox)
		if err != nil {
			return
		}

		// Update message ID
		oldId := msg.ID
		msg.ID = formatMessageId(newMailbox, newUid)

		// Update temporary attachments message ID
		tmpAtts, _ := b.tmpAtts.ListAttachments(user, oldId)
		for _, att := range tmpAtts {
			b.tmpAtts.UpdateAttachmentMessage(user, att.ID, msg.ID)
		}
	}

	return
}

func (b *Messages) DeleteMessage(user, id string) (err error) {
	mailbox, uid, err := parseMessageId(id)
	if err != nil {
		return
	}

	err = b.selectMailbox(user, mailbox)
	if err != nil {
		return
	}

	seqset, _ := imap.NewSeqSet("")
	seqset.AddNum(uid)

	err = b.deleteMessages(user, seqset)
	return
}

func newMessages(conns *conns) *Messages {
	return &Messages{
		conns:     conns,
		tmpAtts:   memory.NewAttachments().(updatableAttachments),
	}
}
