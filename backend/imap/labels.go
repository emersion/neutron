package imap

import (
	"errors"

	"github.com/emersion/neutron/backend"
)

func getLabelID(mailbox string) string {
	lbl := mailbox
	switch mailbox {
	case "INBOX":
		lbl = backend.InboxLabel
	case "Draft", "Drafts":
		lbl = backend.DraftLabel
	case "Sent":
		lbl = backend.SentLabel
	case "Trash":
		lbl = backend.TrashLabel
	case "Spam", "Junk":
		lbl = backend.SpamLabel
	case "Archive", "Archives":
		lbl = backend.ArchiveLabel
	case "Important", "Starred":
		lbl = backend.StarredLabel
	}
	return lbl
}

var colors = []string{
	// Dark
	"#7272a7",
	"#cf5858",
	"#c26cc7",
	"#7569d1",
	"#69a9d1",
	"#5ec7b7",
	"#72bb75",
	"#c3d261",
	"#e6c04c",
	"#e6984c",

	// Light
	"#8989ac",
	"#cf7e7e",
	"#c793ca",
	"#9b94d1",
	"#a8c4d5",
	"#97c9c1",
	"#9db99f",
	"#c6cd97",
	"#e7d292",
	"#dfb28",
}

func getLabelColor(i int) string {
	return colors[i % len(colors)]
}

type Labels struct {
	*conns
}

func (b *Labels) ListLabels(user string) (labels []*backend.Label, err error) {
	mailboxes, err := b.getMailboxes(user)
	if err != nil {
		return
	}

	i := 0
	for _, mailbox := range mailboxes {
		name := mailbox.Name

		if getLabelID(name) != name {
			continue // This is a system mailbox, not a custom one
		}

		labels = append(labels, &backend.Label{
			ID: name,
			Name: name,
			Color: getLabelColor(i),
			Display: 1,
			Order: i,
		})

		i++
	}

	return
}

func (b *Labels) InsertLabel(user string, label *backend.Label) (inserted *backend.Label, err error) {
	labels, err := b.ListLabels(user)
	if err != nil {
		return
	}
	i := len(labels)

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	if err = c.Create(label.Name); err != nil {
		return
	}

	// Refresh mailbox list
	b.clients[user].mailboxes = nil

	inserted = label
	inserted.ID = label.Name
	inserted.Color = getLabelColor(i)
	inserted.Order = i
	return
}

func (b *Labels) UpdateLabel(user string, update *backend.LabelUpdate) (label *backend.Label, err error) {
	label = update.Label

	if label.ID == label.Name {
		return // Nothing to do
	}

	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	if err = c.Rename(label.ID, label.Name); err != nil {
		return
	}

	// Refresh mailbox list
	b.clients[user].mailboxes = nil

	label.ID = label.Name
	return
}

func (b *Labels) DeleteLabel(user, id string) error {
	if err := b.selectMailbox(user, id); err != nil {
		return err
	}

	c, unlock, err := b.getConn(user)
	if err != nil {
		return err
	}
	defer unlock()

	if c.Mailbox.Messages > 0 {
		return errors.New("This label contains mesages, please move all of them before deleting it")
	}

	if err := c.Delete(id); err != nil {
		return err
	}

	// Refresh mailbox list
	b.clients[user].mailboxes = nil
	return nil
}

func newLabels(conns *conns) *Labels {
	return &Labels{
		conns: conns,
	}
}
