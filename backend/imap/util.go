package imap

import (
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"

	"github.com/emersion/neutron/backend"
)

func init() {
	imap.CharsetReader = charset.Reader
}

func formatAttachmentId(mailbox string, uid uint32, part string) string {
	raw := mailbox + "/" + strconv.Itoa(int(uid))
	if part != "" {
		raw += "#" + part
	}
	return base64.URLEncoding.EncodeToString([]byte(raw))
}

func formatMessageId(mailbox string, uid uint32) string {
	return formatAttachmentId(mailbox, uid, "")
}

func parseAttachmentId(id string) (mailbox string, uid uint32, part string, err error) {
	decoded, err := base64.URLEncoding.DecodeString(id)
	if err != nil {
		return
	}

	fstParts := strings.SplitN(string(decoded), "/", 2)
	if len(fstParts) != 2 {
		err = errors.New("Invalid message ID: does not contain separator")
		return
	}
	sndParts := strings.SplitN(fstParts[1], "#", 2)

	uidInt, err := strconv.Atoi(sndParts[0])
	if err != nil {
		return
	}

	mailbox = fstParts[0]
	uid = uint32(uidInt)

	if len(sndParts) == 2 {
		part = sndParts[1]
	}
	return
}

func parseMessageId(id string) (mailbox string, uid uint32, err error) {
	mailbox, uid, _, err = parseAttachmentId(id)
	return
}

func parseMessage(msg *backend.Message, src *imap.Message) {
	msg.Order = int(src.SeqNum)
	msg.Size = int(src.Size)

	for _, flag := range src.Flags {
		switch flag {
		case imap.SeenFlag:
			msg.IsRead = 1
		case imap.AnsweredFlag:
			msg.IsReplied = 1
		case imap.FlaggedFlag:
			msg.Starred = 1
			msg.LabelIDs = append(msg.LabelIDs, backend.StarredLabel)
		case imap.DraftFlag:
			msg.Type = backend.DraftType
		}
	}
}

func writeMessage(w io.Writer, msg *backend.Message) error {
	h := mail.NewHeader()

	mw, err := mail.CreateWriter(w, h)
	if err != nil {
		return err
	}
	defer mw.Close()

	th := mail.NewTextHeader()
	th.SetContentType("text/html", map[string]string{"charset": "utf-8"})
	tw, err := mw.CreateSingleText(th)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(tw, msg.Body); err != nil {
		return err
	}
	tw.Close()

	return nil
}

func writeOutgoingMessage(w io.Writer, msg *backend.OutgoingMessage) error {
	h := mail.NewHeader()

	mw, err := mail.CreateWriter(w, h)
	if err != nil {
		return err
	}
	defer mw.Close()

	th := mail.NewTextHeader()
	th.SetContentType("text/html", map[string]string{"charset": "utf-8"})
	tw, err := mw.CreateSingleText(th)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(tw, msg.Message.Body); err != nil {
		return err
	}
	tw.Close()

	for _, att := range msg.Attachments {
		mimeType := att.MIMEType
		if att.KeyPackets != "" {
			mimeType = "application/pgp"
		}

		ah := mail.NewAttachmentHeader()
		ah.SetContentType(mimeType, map[string]string{"name": att.Name})
		aw, err := mw.CreateAttachment(ah)
		if err != nil {
			return err
		}

		if att.KeyPackets != "" {
			kp, err := base64.StdEncoding.DecodeString(att.KeyPackets)
			if err != nil {
				return err
			}
			if _, err := aw.Write(kp); err != nil {
				return err
			}
		}

		if _, err := aw.Write(att.Data); err != nil {
			return err
		}
		aw.Close()
	}

	return nil
}

func bodyStructureAttachments(structure *imap.BodyStructure) []*backend.Attachment {
	// Non-multipart messages don't contain attachments
	if structure.MimeType != "multipart" || structure.MimeSubType == "alternative" {
		return nil
	}

	var attachments []*backend.Attachment
	for i, part := range structure.Parts {
		if part.MimeType == "multipart" {
			attachments = append(attachments, bodyStructureAttachments(part)...)
			continue
		}

		// Apple Mail doesn't format well header fields
		// First child is message content
		if part.MimeType == "text" && i == 0 {
			continue
		}

		attachments = append(attachments, &backend.Attachment{
			ID: part.Id,
			Name: part.Params["name"],
			MIMEType: part.MimeType + "/" + part.MimeSubType,
			Size: int(part.Size),
		})
	}

	return attachments
}

func getPreferredPart(structure *imap.BodyStructure) (path string, part *imap.BodyStructure) {
	part = structure

	for i, p := range structure.Parts {
		if p.MimeType == "multipart" && p.MimeSubType == "alternative" {
			path, part = getPreferredPart(p)
			path = strconv.Itoa(i+1) + "." + path
		}
		if p.MimeType != "text" {
			continue
		}
		if part.MimeType == "multipart" || p.MimeSubType == "html" {
			part = p
			path = strconv.Itoa(i+1)
		}
	}

	return
}

func parseAttachment(r io.Reader) (att *backend.Attachment, body io.Reader, err error) {
	e, err := message.Read(r)
	if err != nil {
		return
	}

	h := mail.AttachmentHeader{e.Header}

	att = &backend.Attachment{ID: h.Get("Content-Id")}
	body = e.Body

	if t, _, err := h.ContentType(); err == nil {
		att.MIMEType = t
	}
	if name, err := h.Filename(); err == nil {
		att.Name = name
	}
	if size := h.Get("Content-Size"); size != "" {
		att.Size, _ = strconv.Atoi(size)
	}

	return
}

func parseAddress(addr *imap.Address) *backend.Email {
	return &backend.Email{
		Name:    addr.PersonalName,
		Address: addr.MailboxName + "@" + addr.HostName,
	}
}

func parseMailAddress(addr *mail.Address) *backend.Email {
	return &backend.Email{
		Name:    addr.Name,
		Address: addr.Address,
	}
}

func parseAddressList(list []*imap.Address) []*backend.Email {
	emails := make([]*backend.Email, len(list))
	for i, addr := range list {
		emails[i] = parseAddress(addr)
	}
	return emails
}

func parseMailAddressList(list []*mail.Address) []*backend.Email {
	emails := make([]*backend.Email, len(list))
	for i, addr := range list {
		emails[i] = parseMailAddress(addr)
	}
	return emails
}

func parseEnvelope(msg *backend.Message, envelope *imap.Envelope) {
	if !envelope.Date.IsZero() {
		msg.Time = envelope.Date.Unix()
	}

	msg.Subject = envelope.Subject

	if len(envelope.Sender) > 0 {
		msg.Sender = parseAddress(envelope.Sender[0])
	}

	if len(envelope.ReplyTo) > 0 {
		msg.ReplyTo = parseAddress(envelope.ReplyTo[0])
	}

	msg.ToList = parseAddressList(envelope.To)
	msg.CCList = parseAddressList(envelope.Cc)
	msg.BCCList = parseAddressList(envelope.Bcc)
}

func parseMessageHeader(msg *backend.Message, h mail.Header) {
	msg.Subject, _ = h.Subject()
	if from, err := h.AddressList("From"); err == nil && len(from) > 0 {
		msg.Sender = parseMailAddress(from[0])
	}
	if to, err := h.AddressList("To"); err == nil {
		msg.ToList = parseMailAddressList(to)
	}
	if cc, err := h.AddressList("Cc"); err == nil {
		msg.CCList = parseMailAddressList(cc)
	}
	if bcc, err := h.AddressList("Bcc"); err == nil {
		msg.BCCList = parseMailAddressList(bcc)
	}
	if replyTo, err := h.AddressList("Reply-To"); err == nil && len(replyTo) > 0 {
		msg.ReplyTo = parseMailAddress(replyTo[0])
	}
	if t, err := h.Date(); err == nil {
		msg.Time = t.Unix()
	}
}
