package main

import (
	"log"
	"sync"
	"time"
)

type QuotaSnapshot struct {
	Used      int64
	Available int64
	UpdatedAt time.Time
}

type QuotaCache struct {
	mu     sync.Mutex
	ttl    time.Duration
	values map[string]QuotaSnapshot
}

func NewQuotaCache(ttl time.Duration) *QuotaCache {
	return &QuotaCache{ttl: ttl, values: map[string]QuotaSnapshot{}}
}

func (q *QuotaCache) Get(key string) (QuotaSnapshot, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	value, ok := q.values[key]
	if !ok {
		return QuotaSnapshot{}, false
	}
	if q.ttl > 0 && time.Since(value.UpdatedAt) > q.ttl {
		delete(q.values, key)
		return QuotaSnapshot{}, false
	}
	return value, true
}

func (q *QuotaCache) Set(key string, used, available int64) {
	q.mu.Lock()
	q.values[key] = QuotaSnapshot{Used: used, Available: available, UpdatedAt: time.Now()}
	q.mu.Unlock()
}

func (q *QuotaCache) Delete(key string) {
	q.mu.Lock()
	delete(q.values, key)
	q.mu.Unlock()
}

func (a *App) userQuota(user *User, fresh bool) (int64, int64, error) {
	if user == nil || user.NCUserID == nil || user.NCPassword == nil {
		return 0, 0, nil
	}
	key := *user.NCUserID
	if !fresh {
		if value, ok := a.quota.Get(key); ok {
			return value.Used, value.Available, nil
		}
	}
	used, available, err := a.nc.GetQuota(*user.NCUserID, *user.NCPassword)
	if err != nil {
		log.Printf("quota failed: telegram_id=%d nc_user_id=%s err=%v", user.TelegramID, key, err)
		return 0, 0, err
	}
	a.quota.Set(key, used, available)
	return used, available, nil
}

func (a *App) invalidateUserQuota(user *User) {
	if user != nil && user.NCUserID != nil {
		a.quota.Delete(*user.NCUserID)
	}
}
