package imap

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/neutron/backend/util/textproto"
	"github.com/mxk/go-imap/imap"
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

func parseMessageInfo(msg *backend.Message, msgInfo *imap.MessageInfo) {
	msg.Order = int(msgInfo.Seq)
	msg.Size = int(msgInfo.Size)

	if msgInfo.Flags["\\Seen"] {
		msg.IsRead = 1
	}
	if msgInfo.Flags["\\Answered"] {
		msg.IsReplied = 1
	}
	if msgInfo.Flags["\\Flagged"] {
		msg.Starred = 1
		msg.LabelIDs = append(msg.LabelIDs, backend.StarredLabel)
	}
	if msgInfo.Flags["\\Draft"] {
		msg.Type = backend.DraftType
	}
}

func parseEnvelopeAddress(addr []imap.Field) *backend.Email {
	return &backend.Email{
		Name:    textproto.DecodeWord(imap.AsString(addr[0])),
		Address: imap.AsString(addr[2]) + "@" + imap.AsString(addr[3]),
	}
}

func parseEnvelopeAddressList(list []imap.Field) []*backend.Email {
	emails := make([]*backend.Email, len(list))
	for i, field := range list {
		addr := imap.AsList(field)
		emails[i] = parseEnvelopeAddress(addr)
	}
	return emails
}

func parseEnvelope(msg *backend.Message, envelope []imap.Field) {
	// TODO: support more formats (see RFC)
	t, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700 (MST)", imap.AsString(envelope[0]))
	if err != nil {
		t, err = time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", imap.AsString(envelope[0]))
	}
	if err == nil {
		msg.Time = t.Unix()
	}

	msg.Subject = textproto.DecodeWord(imap.AsString(envelope[1]))

	// envelope[2] is From

	senders := imap.AsList(envelope[3])
	if len(senders) > 0 {
		msg.Sender = parseEnvelopeAddress(imap.AsList(senders[0]))
	}

	replyTo := imap.AsList(envelope[4])
	if len(replyTo) > 0 {
		msg.ReplyTo = parseEnvelopeAddress(imap.AsList(replyTo[0]))
	}

	to := imap.AsList(envelope[5])
	msg.ToList = parseEnvelopeAddressList(to)

	cc := imap.AsList(envelope[6])
	msg.CCList = parseEnvelopeAddressList(cc)

	bcc := imap.AsList(envelope[6])
	msg.BCCList = parseEnvelopeAddressList(bcc)

	// envelope[7] is In-Reply-To
	// envelope[8] is Message-Id
}

func parseBodyStructureParams(params []imap.Field) map[string]string {
	result := map[string]string{}

	for i := 0; i < len(params); i += 2 {
		key := imap.AsString(params[i])
		val := imap.AsString(params[i+1])

		result[key] = val
	}

	return result
}

func parseBodyStructure(structure []imap.Field) *textproto.BodyStructure {
	var parse func(structure []imap.Field, id string) *textproto.BodyStructure
	parse = func(structure []imap.Field, id string) *textproto.BodyStructure {
		if imap.TypeOf(structure[0]) == imap.QuotedString {
			if id == "" {
				id = "1"
			}

			// Not a MIME message
			return &textproto.BodyStructure{
				ID:                 id,
				Type:               imap.AsString(structure[0]),
				SubType:            imap.AsString(structure[1]),
				Params:             parseBodyStructureParams(imap.AsList(structure[2])),
				ContentId:          imap.AsString(structure[3]),
				ContentDescription: imap.AsString(structure[4]),
				ContentEncoding:    imap.AsString(structure[5]),
				Size:               int(imap.AsNumber(structure[6])),
			}
		}

		var processedUntil int
		var children []*textproto.BodyStructure
		for i, field := range structure {
			if imap.TypeOf(field) != imap.List {
				processedUntil = i
				break
			}

			childId := strconv.Itoa(i + 1)
			if id != "" {
				childId = id + "." + childId
			}

			child := parse(imap.AsList(field), childId)
			children = append(children, child)
		}

		return &textproto.BodyStructure{
			ID:       id,
			Type:     "multipart",
			SubType:  imap.AsString(structure[processedUntil]),
			Children: children,
		}
	}

	return parse(structure, "")
}
