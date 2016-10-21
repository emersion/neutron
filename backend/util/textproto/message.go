// Provides utilities to parse and format messages.
package textproto

import (
	"bytes"
	"encoding/base64"
	"net/mail"
	"net/textproto"
	//"mime"
	"mime/multipart"
	"mime/quotedprintable"
	//"strings"
	//"io"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/neutron/backend/util/textproto/chunksplit"
)

func ParseMessageHeader(msg *backend.Message, header *mail.Header) {
	msg.Subject = DecodeWord(header.Get("Subject"))

	from, err := header.AddressList("From")
	if err == nil && len(from) > 0 {
		msg.Sender = ParseEmail(from[0])
	}

	to, err := header.AddressList("To")
	if err == nil {
		for _, addr := range to {
			msg.ToList = append(msg.ToList, ParseEmail(addr))
		}
	}

	cc, err := header.AddressList("Cc")
	if err == nil {
		for _, addr := range cc {
			msg.ToList = append(msg.ToList, ParseEmail(addr))
		}
	}

	bcc, err := header.AddressList("Bcc")
	if err == nil {
		for _, addr := range bcc {
			msg.ToList = append(msg.ToList, ParseEmail(addr))
		}
	}

	replyTo, err := header.AddressList("From")
	if err == nil && len(replyTo) > 0 {
		msg.ReplyTo = ParseEmail(replyTo[0])
	}

	time, err := header.Date()
	if err == nil {
		msg.Time = time.Unix()
	}
}

/*func ParseMessagePart(header textproto.MIMEHeader, body io.Reader) (structure *BodyStructure, err error) {
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return
	}

	disp, dispParams, err :=  mime.ParseMediaType(header.Get("Content-Disposition"))
	if err != nil {
		return
	}
	if disp == "attachment" && dispParams["filename"] != "" {
		params["name"] = dispParams["filename"]
	}

	typeParts := strings.SplitN(mediaType, "/", 2)

	structure = &BodyStructure{
		Type: typeParts[0],
		SubType: typeParts[1],
		Params: params,
		ContentId: header.Get("Content-Id"),
		ContentDescription: header.Get("Content-Description"),
		ContentEncoding: header.Get("Content-Encoding"),
		Content: body,
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}

			child, err := ParseMessagePart(p.Header, p)
			if err != nil {
				return nil, err
			}

			structure.Children = append(structure.Children, child)
		}
	}

	return
}*/


func formatMessage(header textproto.MIMEHeader, body string) string {
	return FormatHeader(header) + "\r\n" + body
}

func FormatMessage(msg *backend.Message) string {
	header := GetMessageHeader(msg)
	header.Set("Content-Type", "text/html")
	return formatMessage(header, msg.Body)
}

func FormatOutgoingMessage(msg *backend.OutgoingMessage) string {
	var b bytes.Buffer
	m := multipart.NewWriter(&b)

	var body string
	if msg.MessagePackage != nil {
		body = msg.MessagePackage.Body
	} else {
		body = msg.Message.Body
	}

	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "text/html; charset=UTF-8")
	h.Set("Content-Disposition", "inline")
	h.Set("Content-Transfer-Encoding", "quoted-printable")
	w, _ := m.CreatePart(h)
	enc := quotedprintable.NewWriter(w)
	enc.Write([]byte(body))
	enc.Close()

	for _, att := range msg.Attachments {
		mimeType := att.MIMEType
		if att.KeyPackets != "" {
			mimeType = "application/pgp"
		}

		h := textproto.MIMEHeader{}
		h.Set("Content-Type", mimeType + "; name=\"" + att.Name + "\"")
		h.Set("Content-Disposition", "attachment")
		h.Set("Content-Transfer-Encoding", "base64")

		w, _ := m.CreatePart(h)
		splitter := chunksplit.New("\r\n", 76, w)
		enc := base64.NewEncoder(base64.StdEncoding, splitter)

		if att.KeyPackets != "" {
			kp, _ := base64.StdEncoding.DecodeString(att.KeyPackets)
			enc.Write(kp)
		}
		enc.Write(att.Data)
		enc.Close()
	}

	m.Close()

	mh := GetOutgoingMessageHeader(msg)
	mh.Set("Content-Type", "multipart/mixed; boundary=" + m.Boundary())

	return formatMessage(mh, b.String())
}
