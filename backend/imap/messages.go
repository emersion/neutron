package imap

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/mail"
	"time"

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

func (be *Messages) GetMessage(user, id string) (msg *backend.Message, err error) {
	mailbox, uid, err := parseMessageId(id)
	if err != nil {
		return
	}

	err = be.selectMailbox(user, mailbox)
	if err != nil {
		return
	}

	c, unlock, err := be.getConn(user)
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

	data := <-ch
	if data == nil {
		err = errors.New("No such message")
		return
	}

	msg = &backend.Message{}
	msg.ID = id
	msg.LabelIDs = []string{getLabelID(c.Mailbox.Name)}
	//msg.Header = string(header)
	parseMessage(msg, data)

	// Apply body structure to msg
	msg.Attachments = bodyStructureAttachments(data.BodyStructure)

	// Apply header to msg
	m, err := mail.ReadMessage(data.GetBody("RFC822.HEADER"))
	if err != nil {
		return
	}
	textproto.ParseMessageHeader(msg, &m.Header)

	// Get message content

	path, part := getPreferredPart(data.BodyStructure)

	ch = make(chan *imap.Message, 1)
	if err = c.UidFetch(seqset, []string{"BODY.PEEK["+path+"]"}, ch); err != nil {
		return
	}

	data = <-ch
	if data == nil {
		err = errors.New("No such message body")
		return
	}

	r := decodePart(part, data.GetBody("BODY["+path+"]"))
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return
	}
	msg.Body = string(b)

	// Check if some attachments are encrypted

	/*for _, att := range msg.Attachments {
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
	}*/

	tmpAtts, err := be.tmpAtts.ListAttachments(user, id)
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
	criteria := &imap.SearchCriteria{}
	search := false

	if filter.Begin != 0 {
		criteria.Since = time.Unix(filter.Begin, 0)
		search = true
	}
	if filter.End != 0 {
		criteria.Before = time.Unix(filter.End, 0)
		search = true
	}

	if filter.From != "" {
		criteria.From = filter.From
		search = true
	}
	if filter.To != "" {
		criteria.To = filter.To
		search = true
	}

	if filter.Keyword != "" {
		criteria.Text = filter.Keyword
		search = true
	}

	fetchUid := false
	if search {
		var uids []uint32
		if uids, err = c.UidSearch(criteria); err != nil {
			return
		}

		if len(uids) == 0 {
			return // No result
		}

		set, _ = imap.NewSeqSet("")
		set.AddNum(uids...)
		fetchUid = true
	}

	items := []string{imap.UidMsgAttr, imap.FlagsMsgAttr, imap.SizeMsgAttr, imap.EnvelopeMsgAttr}

	ch := make(chan *imap.Message)
	done := make(chan error, 1)
	go func() {
		if fetchUid {
			done <- c.UidFetch(set, items, ch)
		} else {
			done <- c.Fetch(set, items, ch)
		}
	}()

	for data := range ch {
		msg := &backend.Message{}
		msg.ID = formatMessageId(c.Mailbox.Name, data.Uid)
		msg.LabelIDs = []string{getLabelID(c.Mailbox.Name)}
		parseMessage(msg, data)
		parseEnvelope(msg, data.Envelope)

		msgs = append(msgs, msg)
	}

	// Check command completion status
	if err = <-done; err != nil {
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
		status, err := c.Status(mailbox.Name, []string{imap.MailboxMessages, imap.MailboxUnseen})
		if err != nil {
			return nil, err
		}

		counts = append(counts, &backend.MessagesCount{
			LabelID: getLabelID(status.Name),
			Total:   int(status.Messages),
			Unread:  int(status.Unseen),
		})
	}

	return
}

func (b *Messages) insertMessage(user, mailbox string, flags []string, mail []byte) (uid uint32, err error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	t := time.Time{}
	literal := bytes.NewBuffer(mail)
	if err = c.Append(mailbox, flags, t, literal); err != nil {
		return
	}

	/*if imap.AsString(res.Fields[0]) != "APPENDUID" {
		err = errors.New("APPEND didn't returned an UID (this is not supported for now)")
		return
	}

	uid = imap.AsNumber(res.Fields[2])*/
	uid = 0 // TODO
	return
}

func (b *Messages) InsertMessage(user string, msg *backend.Message) (inserted *backend.Message, err error) {
	mailbox, err := b.getLabelMailbox(user, backend.DraftLabel)
	if err != nil {
		return
	}

	flags := []string{"\\Seen", "\\Draft"}
	mail := textproto.FormatMessage(msg)

	uid, err := b.insertMessage(user, mailbox, flags, []byte(mail))

	inserted = msg
	inserted.ID = formatMessageId(mailbox, uid)
	return
}

func (b *Messages) updateMessageFlags(user string, seqset *imap.SeqSet, flag string, value bool) error {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return err
	}
	defer unlock()

	item := imap.AddFlags
	if !value {
		item = imap.RemoveFlags
	}

	flags := []string{flag}
	return c.UidStore(seqset, item, flags, nil)
}

func (b *Messages) deleteMessages(user string, seqset *imap.SeqSet) error {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return err
	}
	defer unlock()

	flags := []string{imap.DeletedFlag}
	if err := c.UidStore(seqset, imap.AddFlags, flags, nil); err != nil {
		return err
	}

	return c.Expunge(nil) // TODO: use UID EXPUNGE
}

// TODO: only supports moving one single message
func (b *Messages) copyMessages(user string, seqset *imap.SeqSet, mbox string) (uid uint32, err error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	if err = c.UidCopy(seqset, mbox); err != nil {
		return
	}

	/*if imap.AsString(res.Fields[0]) != "COPYUID" {
		err = errors.New("COPY didn't returned an UID (this is not supported for now)")
		return
	}

	uid = imap.AsNumber(res.Fields[2])*/
	uid = 0 // TODO
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
		err = b.updateMessageFlags(user, seqset, imap.SeenFlag, (update.Message.IsRead == 1))
		if err != nil {
			return
		}
	}

	if update.Starred {
		err = b.updateMessageFlags(user, seqset, imap.FlaggedFlag, (update.Message.Starred == 1))
		if err != nil {
			return
		}
	}

	if update.Type {
		err = b.updateMessageFlags(user, seqset, imap.DraftFlag, (update.Message.Type == backend.DraftType))
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

		flags := []string{imap.SeenFlag}
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
