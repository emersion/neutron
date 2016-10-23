package imap

import (
	"errors"
	"io/ioutil"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/go-imap"
)

func (b *Messages) ListAttachments(user, msg string) ([]*backend.Attachment, error) {
	return nil, errors.New("Not yet implemented")
}

func (b *Messages) ReadAttachment(user, id string) (att *backend.Attachment, out []byte, err error) {
	// First, try to get attachment from temporary backend
	att, out, err = b.tmpAtts.ReadAttachment(user, id)
	if err == nil {
		return
	}

	// Not found in tmp backend, get it from the server

	mailbox, uid, partId, err := parseAttachmentId(id)
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

	items := []string{"BODY.PEEK["+partId+"]"}

	messages := make(chan *imap.Message, 1)
	if err = c.UidFetch(seqset, items, messages); err != nil {
		return
	}

	data := <-messages
	if data == nil {
		err = errors.New("No such attachment (cannot find parent message)")
		return
	}

	att, r := parseAttachment(data.GetBody("BODY["+partId+"]"))

	out, err = ioutil.ReadAll(r)
	return
}

func (b *Messages) InsertAttachment(user string, attachment *backend.Attachment, data []byte) (*backend.Attachment, error) {
	return b.tmpAtts.InsertAttachment(user, attachment, data)
}

func (b *Messages) DeleteAttachment(user, id string) error {
	return b.tmpAtts.DeleteAttachment(user, id)
}
