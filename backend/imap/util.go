package imap

import (
	"encoding/base64"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/neutron/backend/util/textproto"
	"github.com/emersion/go-imap"
)

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

func bodyStructureAttachments(structure *imap.BodyStructure) []*backend.Attachment {
	// Non-multipart messages don't contain attachments
	if structure.MimeType != "multipart" || structure.MimeSubType == "alternative" {
		return nil
	}

	var attachments []*backend.Attachment
	for i, part := range structure.Parts {
		if part.Type == "multipart" {
			parseBodyStructure(msg, part)
			continue
		}

		// Apple Mail doesn't format well headers
		// First child is message content
		if part.Type == "text" && i == 0 {
			continue
		}

		attachments = append(attachments, &backend.Attachment{
			ID: s.Id,
			Name: s.Params["name"],
			MIMEType: s.MimeType + "/" + s.MimeSubType,
			Size: int(s.Size),
		})
	}

	return attachments
}

func getPreferredPart(structure *imap.BodyStructure) (path string, part *imap.BodyStructure) {
	part = structure

	for i, p := range structure.Parts {
		if p.MimeType == "multipart" && p.MimeSubType == "alternative" {
			part, path = getPreferredPart(p)
			path = strconv.Itoa(i+1) + "." + path
		}
		if p.Type != "text" {
			continue
		}
		if part.Type == "multipart" || p.SubType == "html" {
			part = p
			path = strconv.Itoa(i+1)
		}
	}

	return
}

func decodePart(part *imap.BodyStructure, r io.Reader) io.Reader {
	return textproto.Decode(r, part.Encoding, part.Params["charset"])
}

func parseAddress(addr *imap.Address) *backend.Email {
	return &backend.Email{
		Name:    textproto.DecodeWord(addr.PersonalName),
		Address: addr.MailboxName + "@" + addr.HostName,
	}
}

func parseAddressList(list []*imap.Address) []*backend.Email {
	emails := make([]*backend.Email, len(list))
	for i, addr := range list {
		emails[i] = parseAddress(addr)
	}
	return emails
}

func parseEnvelope(msg *backend.Message, envelope *imap.Envelope) {
	if !envelope.Date.IsZero() {
		msg.Time = envelope.Date.Unix()
	}

	msg.Subject = envelope.Subject // textproto.DecodeWord()

	if len(envelope.Senders) > 0 {
		msg.Sender = parseAddress(envelope.Senders[0])
	}

	if len(envelope.ReplyTo) > 0 {
		msg.ReplyTo = parseAddress(envelope.ReplyTo[0])
	}

	msg.ToList = parseAddressList(envelope.To)
	msg.CCList = parseAddressList(envelope.Cc)
	msg.BCCList = parseAddressList(envelope.Bcc)
}
