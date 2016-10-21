package imap

import (
	"errors"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/go-imap"
)

type Events struct {
	backend.EventsBackend
	conns *conns
	msgs backend.MessagesBackend
}

func (b *Events) DeleteAllEvents(user string) error {
	err := b.EventsBackend.DeleteAllEvents(user)
	if err != nil {
		return err
	}

	return b.conns.disconnect(user)
}

func (b *Events) processUpdate(u *update) error {
	// TODO: support conversations
	// TODO: support other updates too (EXPUNGE)
	if u.name != "EXISTS" {
		return nil
	}

	user := u.user

	c, unlock, err := b.conns.getConn(user)
	if err != nil {
		return err
	}

	mailbox := c.Mailbox.Name
	seqset, _ := imap.NewSeqSet("")
	seqset.AddNum(u.seqnbr)

	ch := make(chan *imap.Message, 1)
	err = c.Fetch(seqset, []string{imap.UidMsgAttr}, ch)
	unlock()
	if err != nil {
		return err
	}

	m := <-ch
	if m == nil {
		return errors.New("No such message")
	}

	msgId := formatMessageId(mailbox, m.Uid)
	msg, err := b.msgs.GetMessage(user, msgId)
	if err != nil {
		return err
	}

	event := backend.NewMessageDeltaEvent(msg.ID, backend.EventCreate, msg)
	return b.InsertEvent(user, event)
}

func (b *Events) listenUpdates() {
	for {
		u := <-b.conns.updates
		go b.processUpdate(u)
	}
}

func newEvents(conns *conns, events backend.EventsBackend, msgs backend.MessagesBackend) *Events {
	evts := &Events{
		EventsBackend: events,
		conns: conns,
		msgs: msgs,
	}

	go evts.listenUpdates()

	return evts
}
