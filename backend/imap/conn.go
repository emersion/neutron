package imap

import (
	"sync"
	"errors"
	"time"

	"github.com/emersion/go-imap"
	imapclient "github.com/emersion/go-imap/client"
	imapidle "github.com/emersion/go-imap-idle"
	imapquota "github.com/emersion/go-imap-quota"
)

type idleClient struct {*imapidle.Client}
type quotaClient struct {*imapquota.Client}

type conn struct {
	*imapclient.Client
	idleClient
	quotaClient
}

type client struct {
	id string
	conn *conn
	lock sync.Locker
	idle chan struct{}
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
	var c *imapclient.Client
	if b.config.Tls {
		c, err = imapclient.DialTLS(b.config.Host(), nil)
	} else {
		c, err = imapclient.Dial(b.config.Host())
	}
	if err != nil {
		return
	}

	if !b.config.Tls {
		if err = c.StartTLS(nil); err != nil {
			return
		}
	}

	email = username + b.config.Suffix
	if err = c.Login(email, password); err != nil {
		return
	}

	b.clients[username] = &client{
		id: username,
		conn: &conn{
			Client: c,
			idleClient: idleClient{imapidle.NewClient(c)},
			quotaClient: quotaClient{imapquota.NewClient(c)},
		},
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

	if err := c.Close(); err != nil {
		return err
	}

	delete(b.clients, user)
	return nil
}

func (b *conns) getConn(user string) (*conn, func(), error) {
	clt, ok := b.clients[user]
	if !ok {
		return nil, nil, errors.New("No such user")
	}
	c := clt.conn
	lock := clt.lock

	lock.Lock()

	if c.State & imap.ConnectedState == 0 {
		delete(b.clients, user)

		// Connection closed, reconnect
		if _, err := b.connect(user, clt.password); err != nil {
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
	if clt.idle != nil {
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
	if clt.idle != nil {
		return nil
	}

	c := clt.conn

	mailbox := "INBOX"
	if c.Mailbox != nil && c.Mailbox.Name != mailbox {
		if _, err := c.Select(mailbox, false); err != nil {
			return err
		}
	}

	clt.lock.Lock()
	defer clt.lock.Unlock()

	done := make(chan error, 1)
	clt.idle = make(chan struct{})
	go func() {
		done <- c.Idle(clt.idle)
	}()

	reset := time.After(20 * time.Minute)

	for {
		select {
		case status := <-c.MailboxUpdates:
			u := &update{
				user: clt.id,
				name: "EXISTS",
				seqnbr: status.Messages,
			}

			select {
			case b.updates <- u:
			default:
			}
		case seqNum := <-c.Expunges:
			u := &update{
				user: clt.id,
				name: "EXPUNGE",
				seqnbr: seqNum,
			}

			select {
			case b.updates <- u:
			default:
			}
		//case msg := <-c.MessageUpdates:
		case <-reset:
			// Reset idle (RFC 2177 recommends 29 min max)
			if err := b.cancelIdle(clt); err != nil {
				return err
			}

			return b.idle(clt)
		case err := <-done:
			return err
		}
	}
}

func (b *conns) cancelIdle(clt *client) error {
	if clt.idleTimer != nil {
		clt.idleTimer.Stop()
	}

	if clt.idle != nil {
		close(clt.idle)
		clt.idle = nil
	}

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

	mailboxes := make(chan *imap.MailboxInfo)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	client.mailboxes = nil
	for info := range mailboxes {
		client.mailboxes = append(client.mailboxes, info)
	}

	return client.mailboxes, <-done
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
