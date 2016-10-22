package imap

import (
	"errors"

	"github.com/emersion/neutron/backend"
	"github.com/emersion/go-imap-quota"
)

// IMAP backend cannot upate users, so when requesting to update it will just return silently.
// When inserting a new user, it will just check that the user already exist on the IMAP server.
type Users struct {
	*conns

	users map[string]*backend.User
}

func (b *Users) getQuota(user *backend.User) error {
	c, unlock, err := b.getConn(user.ID)
	if err != nil {
		return err
	}
	defer unlock()

	if !c.SupportsQuota() {
		return nil
	}

	quotas, err := c.GetQuotaRoot("INBOX")
	if err != nil {
		return err
	}

	// TODO: support multiple quotas?
	if len(quotas) > 0 {
		if usage, ok := quotas[0].Resources[quota.ResourceStorage]; ok {
			user.UsedSpace = int(usage[0]) * 1024
			user.MaxSpace = int(usage[1]) * 1024
		}
	}

	return nil
}

func (b *Users) GetUser(id string) (user *backend.User, err error) {
	user, ok := b.users[id]
	if !ok {
		err = errors.New("No such user")
	}
	return
}

func (b *Users) Auth(username, password string) (user *backend.User, err error) {
	id := username

	// User already logged in, just checking password
	if client, ok := b.clients[id]; ok {
		if client.password != password {
			err = errors.New("Invalid username or password")
		} else {
			user = b.users[id]
		}
		return
	}

	email, err := b.connect(username, password)
	if err != nil {
		return
	}

	user = &backend.User{
		ID: id,
		Name: username,
		DisplayName: username,
		Addresses: []*backend.Address{
			&backend.Address{
				ID: username,
				Email: email,
				Send: 1,
				Receive: 1,
				Status: 1,
				Type: 1,
			},
		},
	}

	b.getQuota(user)

	b.users[user.ID] = user

	return
}

// Cannot check if a username is available, always return true
func (b *Users) IsUsernameAvailable(username string) (bool, error) {
	return true, nil
}

func (b *Users) InsertUser(u *backend.User, password string) (*backend.User, error) {
	return b.Auth(u.Name, password)
}

func (b *Users) UpdateUser(update *backend.UserUpdate) error {
	return nil
}

func (b *Users) UpdateUserPassword(id, current, new string) error {
	return errors.New("Cannot update user password with IMAP backend")
}

func newUsers(conns *conns) *Users {
	return &Users{
		conns: conns,

		users: map[string]*backend.User{},
	}
}
