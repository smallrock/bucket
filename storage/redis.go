package storage

import (
	"github.com/go-redis/redis"
	"strconv"
	"errors"
	"fmt"
)

const (
	// Redis and our redis client library allow lua scripting. We use the following script to conditionally decrement
	// a key-value pair by a given amount. This allows us not only to do both actions in a single round-trip to the
	// database but provide a lock-free implementation for logic that would otherwise be unsafe in a concurrent environment.
	//
	// Consider if we make two calls: .Get() and .Decr() with a conditional that .Decr() should only be called if .Get()
	// returns a certain amount... what if the value had been modified by a separate client during the
	// conditional statement? Then we might decrement the value incorrectly.
	//
	// Notably lua scripting is less performent then normal Redis commands thus this library should not share a database
	// with a super high-performance requirement
	//
	// Notice the use of tonumber() in Lua because everything Redis takes in and spits out is a string
	luaGetAndDecr = `
		local key = KEYS[1]
		local amount = tonumber(ARGV[1])
		local count = tonumber(redis.call("get", key))

		if count >= amount then
			return redis.call("DECRBY", key, amount)
		else
			error("Insufficient tokens/")
		end
	`

	// used for storage.TakeAll(), returns a conditional amount of tokens representing all the tokens
	luaGetAllAndDecr = `
		local key = KEYS[1]
		local count = tonumber(redis.call("get", key))
		redis.call("DECRBY", key, count)
		return count
	`
)

type RedisStorage struct {
	Client *redis.Client
}

func (rs *RedisStorage) Ping() error {
	return rs.Client.Ping().Err()
}

// bucket.Create will create a new bucket with the given parameters if one does not exist, if no bucket can be created it will return an error
func (rs *RedisStorage) Create(name string, capacity int) error {
	// check if name exists as a key of redis
	strTokensCount, err := rs.Client.Get(name).Result()

	// if the name key does not exist in redis create it with the value of capacity and return nil (or an error if redis throws one)
	if err == redis.Nil || len(strTokensCount) == 0 {
		// the last param 0 indicates the key will never expire
		return rs.Client.Set(name, capacity, 0).Err()
	}

	// if the found value is a string which cannot be converted to an integer assume this key is protected and return an error
	tokensCount, err := strconv.Atoi(strTokensCount)

	if err != nil {
		return err
	}

	// if the following value is converted to the integer 0 assume this was a programming mistake and the programmer
	// was not aware that this key already existed, return an error
	if tokensCount == 0 {
		return errors.New("Bucket exists in redis but contains a value of 0. Try putting tokens back into this bucket.")
	}

	return nil
}

// Executes a lua script which decrements the token value by tokensDesired if tokensDesired >= the token value.
func (rs *RedisStorage) Take(bucketName string, tokens int) error {
	return rs.Client.Eval(luaGetAndDecr, []string{bucketName}, tokens).Err()
}

// returns a conditional amount of tokens representing all the tokens
func (rs *RedisStorage) TakeAll(bucketName string) (int, error) {
	raw, err := rs.Client.Eval(luaGetAllAndDecr, []string{bucketName}).Result()

	if err != nil {
		return 0, err
	}

	// if the found value is a string which cannot be converted to an integer assume this key is protected and return an error
	tokenValue, ok := raw.(int64)

	if !ok {
		return 0, errors.New(fmt.Sprintf("Failed to convert %v to int", raw))
	}

	return int(tokenValue), nil
}


func (rs *RedisStorage) Set(bucketName string, tokens int) error {
	// 0 indicates no expiration
	return rs.Client.Set(bucketName, tokens, 0).Err()
}

// Increment the token value by a given amount.
func (rs *RedisStorage) Put(bucketName string, tokens int) error {
	return rs.Client.IncrBy(bucketName, int64(tokens)).Err()
}

// Return the token value of a given bucket.
func (rs *RedisStorage) Count(bucketName string) (int, error) {
	count, err := rs.Client.Get(bucketName).Int64()
	return int(count), err
}