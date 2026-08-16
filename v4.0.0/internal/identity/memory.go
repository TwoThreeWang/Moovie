package identity

import (
	"context"
	"fmt"
	"sync"
)

type MemoryStore struct {
	mu     sync.RWMutex
	nextID int
	users  []User
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{nextID: 1} }

func (store *MemoryStore) FindByEmail(_ context.Context, email string) (*User, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, user := range store.users {
		if user.Email == email {
			copy := user
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) FindByID(_ context.Context, id int) (*User, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	for _, user := range store.users {
		if user.ID == id {
			copy := user
			return &copy, nil
		}
	}
	return nil, nil
}

func (store *MemoryStore) ListUsers(_ context.Context) ([]User, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return append([]User(nil), store.users...), nil
}

func (store *MemoryStore) Create(_ context.Context, user User) (*User, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, existing := range store.users {
		if existing.Email == user.Email || existing.Username == user.Username {
			return nil, fmt.Errorf("user already exists")
		}
	}
	user.ID = store.nextID
	store.nextID++
	store.users = append(store.users, user)
	copy := user
	return &copy, nil
}

func (store *MemoryStore) UpdateUsername(_ context.Context, userID int, username string) error {
	return store.update(userID, func(user *User) { user.Username = username })
}

func (store *MemoryStore) UpdateEmail(_ context.Context, userID int, email string) error {
	return store.update(userID, func(user *User) { user.Email = email })
}

func (store *MemoryStore) UpdatePassword(_ context.Context, userID int, passwordHash string) error {
	return store.update(userID, func(user *User) { user.PasswordHash = passwordHash })
}

func (store *MemoryStore) UpdateIsPublic(_ context.Context, userID int, isPublic bool) error {
	return store.update(userID, func(user *User) { user.IsPublic = isPublic })
}

func (store *MemoryStore) UpdateAvatar(_ context.Context, userID int, avatar string) error {
	return store.update(userID, func(user *User) { user.Avatar = avatar })
}

func (store *MemoryStore) UpdateRole(_ context.Context, userID int, role string) error {
	return store.update(userID, func(user *User) { user.Role = role })
}

func (store *MemoryStore) Delete(_ context.Context, userID int) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.users {
		if store.users[index].ID == userID {
			store.users = append(store.users[:index], store.users[index+1:]...)
			break
		}
	}
	return nil
}

func (store *MemoryStore) UpdateDoubanUserID(_ context.Context, userID int, doubanUserID string) error {
	return store.update(userID, func(user *User) { user.DoubanUserID = doubanUserID })
}

func (store *MemoryStore) ListBoundDoubanUsers(_ context.Context) ([]User, error) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	users := make([]User, 0)
	for _, user := range store.users {
		if user.DoubanUserID != "" {
			users = append(users, user)
		}
	}
	return users, nil
}

func (store *MemoryStore) update(userID int, mutate func(*User)) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	for index := range store.users {
		if store.users[index].ID == userID {
			mutate(&store.users[index])
			return nil
		}
	}
	return fmt.Errorf("user not found")
}
