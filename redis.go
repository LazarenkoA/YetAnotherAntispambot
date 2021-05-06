package main

import (
	"fmt"
	"github.com/garyburd/redigo/redis"
	"time"
)

type Redis struct {
	pool *redis.Pool
}

func (R *Redis) Create(stringConnect string) (*Redis, error) {
	R.initPool(stringConnect)
	return R, R.pool.TestOnBorrow(R.pool.Get(), time.Now())
}

func (R *Redis) initPool(stringConnect string) {
	R.pool = &redis.Pool{
		MaxIdle:     3,
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
	defer R.pool.Close()

	exists, err := redis.Bool(R.pool.Get().Do("EXISTS", key))
	if err != nil {
		fmt.Println("Redis. Ошибка при выполнении KeyExists")
	}

	return exists
}

func (R *Redis) Keys() []string {
	defer R.pool.Close()

	keys, err := redis.Strings(R.pool.Get().Do("KEYS", "*"))
	if err != nil && err != redis.ErrNil {
		fmt.Printf("Redis. Ошибка при выполнении KEYS. %v\n", err)
	}

	return keys
}

func (R *Redis) Count(key string) int {
	defer R.pool.Close()

	count, err := redis.Int(R.pool.Get().Do("SCARD", key))
	if err != nil {
		fmt.Println("Redis. Ошибка при выполнении Count")
	}
	return count
}

func (R *Redis) Delete(key string) error {
	defer R.pool.Close()

	_, err := R.pool.Get().Do("DEL", key)
	if err != nil {
		fmt.Println("Redis. Ошибка при выполнении Delete")
	}
	return err
}

// Установка значения
// ttl - через сколько будет очищено значение (минимальное значение 1 секунда)
func (R *Redis) Set(key, value string, ttl time.Duration) error {
	defer R.pool.Close()

	param := []interface{}{key, value}
	if ttl >= time.Second {
		param = append(param, "EX", ttl.Seconds())
	}

	_, err := R.pool.Get().Do("SET", param...)
	if err != nil {
		fmt.Printf("Redis. Ошибка при выполнении Set. %v\n", err)
	}
	return err
}

func (R *Redis) Get(key string) (string, error) {
	defer R.pool.Close()

	v, err := redis.String(R.pool.Get().Do("GET", key))
	if err != nil && err != redis.ErrNil {
		fmt.Printf("Redis. Ошибка при выполнении Get. %v\n", err)
	}
	return v, err
}

func (R *Redis) DeleteItems(key, value string) error {
	defer R.pool.Close()

	_, err := R.pool.Get().Do("SREM", key, value)
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении DeleteItems")
	}
	return err
}

func (R *Redis) Items(key string) []string {
	defer R.pool.Close()

	items, err := redis.Strings(R.pool.Get().Do("SMEMBERS", key))
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении Items")
	}
	return items
}

func (R *Redis) LPOP(key string) string {
	defer R.pool.Close()

	item, err := redis.String(R.pool.Get().Do("LPOP", key))
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении LPOP")
	}
	return item
}

func (R *Redis) RPUSH(key, value string) error {
	defer R.pool.Close()

	_, err := R.pool.Get().Do("RPUSH", key, value)
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении RPUSH")
	}
	return err
}

// Добавляет в неупорядоченную коллекцию значение
func (R *Redis) AppendItems(key, value string) {
	defer R.pool.Close()

	_, err := R.pool.Get().Do("SADD", key, value)
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении AppendItems")
	}
}

func (R *Redis) SetMap(key string, value map[string]string) {
	defer R.pool.Close()

	for k, v := range value {
		_, err := R.pool.Get().Do("HSET", key, k, v)
		if err != nil {
			fmt.Println("Redis. Ошибка при выполнении SetMap")
			break
		}
	}
}

func (R *Redis) StringMap(key string) (map[string]string, error) {
	defer R.pool.Close()

	value, err := redis.StringMap(R.pool.Get().Do("HGETALL", key))
	if err != nil && err != redis.ErrNil {
		fmt.Println("Redis. Ошибка при выполнении StringMap")
	}
	return value, err
}

// Начало транзакции
func (R *Redis) Begin() {
	defer R.pool.Close()

	R.pool.Get().Do("MULTI")
}

// Фиксация транзакции
func (R *Redis) Commit() {
	defer R.pool.Close()

	R.pool.Get().Do("EXEC")
}

// Откат транзакции
func (R *Redis) Rollback() {
	defer R.pool.Close()

	R.pool.Get().Do("DISCARD")
}
