package imap

import (
	"sync"
	"errors"
	"time"

	"github.com/mxk/go-imap/imap"
)

type client struct {
	id string
	conn *imap.Client
	lock sync.Locker
	idle bool
	idleTimer *time.Timer
	password string
	mailboxes []*imap.MailboxInfo
}

type update struct {
	user string
	name string
	seqnbr uint32
}

type conns struct {
	config *Config
	clients map[string]*client
	updates chan *update
}

func (b *conns) connect(username, password string) (email string, err error) {
	var c *imap.Client
	if b.config.Tls {
		c, err = imap.DialTLS(b.config.Host(), nil)
	} else {
		c, err = imap.Dial(b.config.Host())
	}
	if err != nil {
		return
	}

	//c.SetLogMask(imap.LogAll)

	if !b.config.Tls {
		if !c.Caps["STARTTLS"] {
			err = errors.New("IMAP server doesn't support STARTTLS")
			return
		}

		_, err = c.StartTLS(nil)
		if err != nil {
			return
		}
	}

	email = username + b.config.Suffix
	_, err = c.Login(email, password)
	if err != nil {
		return
	}

	b.clients[username] = &client{
		id: username,
		conn: c,
		lock: &sync.Mutex{},
		password: password,
	}
	return
}

func (b *conns) disconnect(user string) error {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return err
	}
	defer unlock()

	_, err = c.Close(false)
	if err != nil {
		return err
	}

	delete(b.clients, user)
	return nil
}

func (b *conns) getConn(user string) (*imap.Client, func(), error) {
	clt, ok := b.clients[user]
	if !ok {
		return nil, nil, errors.New("No such user")
	}
	c := clt.conn
	lock := clt.lock

	lock.Lock()

	state := c.State()
	if state == imap.Logout || state == imap.Closed {
		delete(b.clients, user)

		// Connection closed, reconnect
		_, err := b.connect(user, clt.password)
		if err != nil {
			delete(b.clients, user)
			lock.Unlock()
			return nil, nil, err
		}

		clt = b.clients[user]
		c = clt.conn
		lock = clt.lock

		lock.Lock()
	}

	b.cancelIdle(clt)

	unlock := func() {
		b.scheduleIdle(clt)
		lock.Unlock()
	}

	return c, unlock, nil
}

func (b *conns) scheduleIdle(clt *client) {
	if clt.idle {
		return
	}

	if clt.idleTimer != nil {
		clt.idleTimer.Stop()
	}

	clt.idleTimer = time.AfterFunc(10 * time.Second, func() {
		b.idle(clt)
	})
}

func (b *conns) idle(clt *client) error {
	if clt.idle {
		return nil
	}

	c := clt.conn

	mailbox := "INBOX"
	if c.Mailbox != nil && c.Mailbox.Name != mailbox {
		_, err := c.Select(mailbox, false)
		if err != nil {
			return err
		}
	}

	_, err := c.Idle()
	if err != nil {
		return err
	}

	clt.idle = true

	reset := time.After(20 * time.Minute)

	for {
		select {
		case <-time.After(10 * time.Second):
			// Client stopped idling
			if !clt.idle {
				return nil
			}

			clt.lock.Lock()

			err = c.Recv(0)
			if err == imap.ErrTimeout {
				// Nothing was received
				clt.lock.Unlock()
				break
			}
			if err != nil {
				clt.lock.Unlock()
				return err
			}

			// Copy data
			data := []*imap.Response{}
			data = append(data, c.Data...)
			c.Data = nil

			clt.lock.Unlock()

			for _, res := range data {
				if res.Type != imap.Data {
					continue
				}
				if len(res.Fields) != 2 {
					continue
				}

				// Send update (non-blocking)
				u := &update{
					user: clt.id,
					name: imap.AsString(res.Fields[1]),
					seqnbr: imap.AsNumber(res.Fields[0]),
				}

				select {
				case b.updates <- u:
				default:
				}
			}
		case <-reset:
			// Reset idle (RFC 2177 recommends 29 min max)
			err = b.cancelIdle(clt)
			if err != nil {
				return err
			}

			return b.idle(clt)
		}
	}

	return nil
}

func (b *conns) cancelIdle(clt *client) error {
	if !clt.idle {
		if clt.idleTimer != nil {
			clt.idleTimer.Stop()
		}
		return nil
	}

	c := clt.conn

	_, err := c.IdleTerm()
	if err != nil {
		return err
	}
	clt.idle = false

	return nil
}

// Allow other backends (e.g. a SMTP backend) to access users' password.
func (b *conns) GetPassword(user string) (string, error) {
	if client, ok := b.clients[user]; ok {
		return client.password, nil
	}
	return "", errors.New("No password stored for this user")
}

func (b *conns) getMailboxes(user string) ([]*imap.MailboxInfo, error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return nil, err
	}
	defer unlock()

	client := b.clients[user]

	// Mailboxes list already retrieved
	if len(client.mailboxes) > 0 {
		return client.mailboxes, nil
	}

	cmd, _, err := wait(c.List("", "%"))
	if err != nil {
		return nil, err
	}

	// Retrieve mailboxes info and subscribe to them
	client.mailboxes = make([]*imap.MailboxInfo, len(cmd.Data))
	for i, rsp := range cmd.Data {
		mailboxInfo := rsp.MailboxInfo()
		client.mailboxes[i] = mailboxInfo
	}

	return client.mailboxes, nil
}

func (b *conns) getLabelMailbox(user, label string) (mailbox string, err error) {
	mailboxes, err := b.getMailboxes(user)
	if err != nil {
		return
	}

	mailbox = label
	for _, m := range mailboxes {
		if getLabelID(m.Name) == label {
			mailbox = m.Name
			break
		}
	}

	return
}

func (b *conns) selectMailbox(user, mailbox string) (err error) {
	c, unlock, err := b.getConn(user)
	if err != nil {
		return
	}
	defer unlock()

	if c.Mailbox == nil || c.Mailbox.Name != mailbox {
		_, err = c.Select(mailbox, false)
		if err != nil {
			return
		}
	}

	return
}

func (b *conns) selectLabelMailbox(user, label string) (err error) {
	mailbox, err := b.getLabelMailbox(user, label)
	if err != nil {
		return
	}

	return b.selectMailbox(user, mailbox)
}

func newConns(config *Config) *conns {
	return &conns{
		config: config,

		clients: map[string]*client{},
		updates: make(chan *update),
	}
}
