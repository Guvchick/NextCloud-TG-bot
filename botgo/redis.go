package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type StateStore struct {
	mu     sync.Mutex
	values map[int64]State
	redis  *RedisClient
}

func NewStateStore(redis *RedisClient) *StateStore {
	return &StateStore{values: map[int64]State{}, redis: redis}
}

func (s *StateStore) Set(id int64, st State) {
	s.mu.Lock()
	s.values[id] = st
	s.mu.Unlock()
	if s.redis != nil {
		raw, _ := json.Marshal(st)
		if err := s.redis.SetEX("state:"+strconv.FormatInt(id, 10), string(raw), 6*time.Hour); err != nil {
			log.Printf("redis state set failed: %v", err)
		}
	}
}

func (s *StateStore) Get(id int64) (State, bool) {
	if s.redis != nil {
		raw, err := s.redis.Get("state:" + strconv.FormatInt(id, 10))
		if err == nil && raw != "" {
			var st State
			if json.Unmarshal([]byte(raw), &st) == nil {
				return st, true
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.values[id]
	return st, ok
}

func (s *StateStore) Clear(id int64) {
	s.mu.Lock()
	delete(s.values, id)
	s.mu.Unlock()
	if s.redis != nil {
		if err := s.redis.Del("state:" + strconv.FormatInt(id, 10)); err != nil {
			log.Printf("redis state del failed: %v", err)
		}
	}
}

type RedisClient struct {
	addr     string
	password string
	db       string
}

func NewRedisClient(rawURL string) (*RedisClient, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	addr := parsed.Host
	if !strings.Contains(addr, ":") {
		addr += ":6379"
	}
	password, _ := parsed.User.Password()
	db := strings.TrimPrefix(parsed.Path, "/")
	return &RedisClient{addr: addr, password: password, db: db}, nil
}

func (r *RedisClient) SetEX(key, value string, ttl time.Duration) error {
	_, err := r.do("SETEX", key, strconv.Itoa(int(ttl.Seconds())), value)
	return err
}

func (r *RedisClient) Get(key string) (string, error) {
	value, err := r.do("GET", key)
	if err != nil {
		return "", err
	}
	if value == nil {
		return "", nil
	}
	return fmt.Sprint(value), nil
}

func (r *RedisClient) Del(key string) error {
	_, err := r.do("DEL", key)
	return err
}

func (r *RedisClient) do(args ...string) (any, error) {
	conn, err := net.DialTimeout("tcp", r.addr, 3*time.Second)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	if r.password != "" {
		if _, err := writeRedisCommand(conn, "AUTH", r.password); err != nil {
			return nil, err
		}
		if _, err := readRedisReply(reader); err != nil {
			return nil, err
		}
	}
	if r.db != "" && r.db != "0" {
		if _, err := writeRedisCommand(conn, "SELECT", r.db); err != nil {
			return nil, err
		}
		if _, err := readRedisReply(reader); err != nil {
			return nil, err
		}
	}
	if _, err := writeRedisCommand(conn, args...); err != nil {
		return nil, err
	}
	return readRedisReply(reader)
}

func writeRedisCommand(w io.Writer, args ...string) (int, error) {
	var b strings.Builder
	b.WriteString("*")
	b.WriteString(strconv.Itoa(len(args)))
	b.WriteString("\r\n")
	for _, arg := range args {
		b.WriteString("$")
		b.WriteString(strconv.Itoa(len(arg)))
		b.WriteString("\r\n")
		b.WriteString(arg)
		b.WriteString("\r\n")
	}
	return io.WriteString(w, b.String())
}

func readRedisReply(r *bufio.Reader) (any, error) {
	prefix, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, errors.New(line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '$':
		size, _ := strconv.Atoi(line)
		if size < 0 {
			return nil, nil
		}
		buf := make([]byte, size+2)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		return string(buf[:size]), nil
	case '*':
		count, _ := strconv.Atoi(line)
		items := make([]any, 0, count)
		for i := 0; i < count; i++ {
			item, err := readRedisReply(r)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return items, nil
	default:
		return nil, fmt.Errorf("unknown redis reply prefix %q", prefix)
	}
}

