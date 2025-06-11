package db

import (
	"github.com/pkg/errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/garyburd/redigo/redis"
)

type Redis struct {
	pool   *redis.Pool
	logger *slog.Logger
}

func (R *Redis) Create(stringConnect string) (*Redis, error) {
	R.initPool(stringConnect)
	R.logger = slog.Default().With("name", "redis")

	return R, R.pool.TestOnBorrow(R.pool.Get(), time.Now())
}

func (R *Redis) initPool(stringConnect string) {
	R.pool = &redis.Pool{
		MaxIdle:     10,
		IdleTimeout: time.Second * 10,
		Dial: func() (redis.Conn, error) {
			c, err := redis.DialURL(stringConnect)
			if err != nil {
				return nil, err
			} else {
				return c, err
			}
		},
		TestOnBorrow: func(c redis.Conn, t time.Time) error {
			_, err := c.Do("PING")
			return err
		},
	}
}

func (R *Redis) KeyExists(key string) bool {
	exists, err := redis.Bool(R.pool.Get().Do("EXISTS", key))
	if err != nil {
		R.logger.Error("ошибка при выполнении KeyExists", "error", err)
	}

	return exists
}

func (R *Redis) KeysMask(mask string) []string {
	keys, err := redis.Strings(R.pool.Get().Do("KEYS", mask))
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении KEYS", "error", err)
	}

	return keys
}

func (R *Redis) Keys() []string {
	return R.KeysMask("*")
}

func (R *Redis) Count(key string) int {
	count, err := redis.Int(R.pool.Get().Do("SCARD", key))
	if err != nil {
		R.logger.Error("ошибка при выполнении Count", "error", err)
	}
	return count
}

func (R *Redis) Delete(key string) error {
	_, err := R.pool.Get().Do("DEL", key)
	if err != nil {
		R.logger.Error("ошибка при выполнении Delete", "key", key, "error", err)
	}
	return err
}

// Установка значения
// ttl - через сколько будет очищено значение (минимальное значение 1 секунда)
func (R *Redis) Set(key, value string, ttl time.Duration) error {
	param := []interface{}{key, value}
	if ttl >= time.Second {
		param = append(param, "EX", ttl.Seconds())
	}

	_, err := R.pool.Get().Do("SET", param...)
	if err != nil {
		R.logger.Error("ошибка при выполнении Set", "key", key, "value", value, "error", err)
	}
	return err
}

func (R *Redis) Get(key string) (string, error) {
	v, err := redis.String(R.pool.Get().Do("GET", key))
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении Get", "key", key, "error", err)
	}
	return v, err
}

func (R *Redis) DeleteItems(key, value string) error {
	_, err := R.pool.Get().Do("SREM", key, value)
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении DeleteItems", "key", key, "error", err)
	}
	return err
}

func (R *Redis) Items(key string) ([]string, error) {
	tmp, err := R.pool.Get().Do("SMEMBERS", key)
	_ = tmp

	items, err := redis.Strings(R.pool.Get().Do("SMEMBERS", key))
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении Items", "key", key, "error", err)
	}
	return items, err
}

func (R *Redis) LPOP(key string) string {
	item, err := redis.String(R.pool.Get().Do("LPOP", key))
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении LPOP", "key", key, "error", err)
	}
	return item
}

func (R *Redis) RPUSH(key, value string) error {
	_, err := R.pool.Get().Do("RPUSH", key, value)
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении RPUSH", "key", key, "value", value, "error", err)
	}
	return err
}

// Добавляет в неупорядоченную коллекцию значение
func (R *Redis) AppendItems(key, value string) {
	_, err := R.pool.Get().Do("SADD", key, value)
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении AppendItems", "key", key, "value", value, "error", err)
	}
}

func (R *Redis) SetMap(key string, value map[string]string) {
	for k, v := range value {
		if !utf8.ValidString(v) {
			R.logger.Warn("string value not UTF-8", "key", k, "value", v)
		}

		_, err := R.pool.Get().Do("HSET", key, k, v)
		if err != nil {
			R.logger.Error("ошибка при выполнении SetMap", "key", key, "value", value, "error", err)
			break
		}
	}

}

func (R *Redis) StringMap(key string) (map[string]string, error) {
	value, err := redis.StringMap(R.pool.Get().Do("HGETALL", key))
	if err != nil && !errors.Is(err, redis.ErrNil) {
		R.logger.Error("ошибка при выполнении StringMap", "key", key, "error", err)
	}
	return value, err
}

// Начало транзакции
func (R *Redis) Begin() {
	R.pool.Get().Do("MULTI")
}

// Фиксация транзакции
func (R *Redis) Commit() {
	R.pool.Get().Do("EXEC")
}

// Откат транзакции
func (R *Redis) Rollback() {
	R.pool.Get().Do("DISCARD")
}
