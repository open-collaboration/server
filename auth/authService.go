// NOTE: take a look at the projects redis documentation (docs/redis.md)
// to better understand how session tokens are stored.

package auth

import (
	"context"
	"errors"
	"fmt"
	"github.com/apex/log"
	"github.com/go-redis/redis/v8"
	"github.com/gofrs/uuid"
	"github.com/open-collaboration/server/users"
	"gorm.io/gorm"
	"time"
)

var ErrInvalidSessionToken = errors.New("invalid session key")
var ErrWrongPassword = errors.New("wrong password")

type Service interface {
	// Authenticate a user with username or email and a password.
	// Returns ErrUserNotFound if a user with a matching username/email and password pair cannot be found.
	// Returns ErrWrongPassword if the hashed password does not equal the user's stored password hash.
	AuthenticateUser(ctx context.Context, authUser LoginDto) (*users.User, error)

	// Check if a session exists and, if it does, return the session's user.
	// Returns ErrInvalidSessionToken if the session does not exist.
	AuthenticateSession(ctx context.Context, sessionKey string) (uint, error)

	// Create a session key for a user. The session key will last 30 days.
	CreateSession(ctx context.Context, userId uint) (string, error)

	// Invalidate (delete) all sessions of a user.
	InvalidateSessions(ctx context.Context, userId uint) error
}

type serviceImpl struct {
	Db           *gorm.DB
	Redis        *redis.Client
	UsersService users.Service
}

func NewService(db *gorm.DB, redisDb *redis.Client, usersService users.Service) Service {
	return &serviceImpl{
		Db:           db,
		Redis:        redisDb,
		UsersService: usersService,
	}
}

func (s *serviceImpl) AuthenticateUser(ctx context.Context, authUser LoginDto) (*users.User, error) {
	logger := log.FromContext(ctx).
		WithField("username", authUser.UsernameOrEmail)

	logger.Debug("Authenticating user")
	logger.Debug("Searching for user")

	user, err := s.UsersService.FindUserByUsernameOrEmail(ctx, authUser.UsernameOrEmail)
	if err != nil {
		logger.WithError(err).Error("Failed to authenticate user")
	}

	logger.Debug("Comparing passwords")

	passwordMatch, err := user.ComparePassword(authUser.Password)
	if err != nil {
		logger.WithError(err).Error("Error comparing passwords")

		return nil, err
	} else if passwordMatch {
		logger.Debug("Passwords match, user authenticated")

		return user, nil
	} else {
		logger.Debug("Wrong password")

		return nil, ErrWrongPassword
	}
}

func (s *serviceImpl) AuthenticateSession(ctx context.Context, sessionKey string) (uint, error) {
	logger := log.FromContext(ctx)

	logger.Debug("Checking for session in redis")

	redisKey := sessionRedisKey(sessionKey)
	userId, err := s.Redis.Get(ctx, redisKey).Int()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			logger.Debug("Session does not exist")

			return 0, ErrInvalidSessionToken
		} else {
			logger.WithError(err).Error("Failed to check for session in redis")

			return 0, err
		}
	}

	logger.Debug("Session is valid")

	return uint(userId), nil
}

func (s *serviceImpl) CreateSession(ctx context.Context, userId uint) (string, error) {
	logger := log.FromContext(ctx)

	logger.
		WithField("userId", userId).
		Debug("Creating session key")

	// Using uuid is as session key is safe here because it uses the rand
	// package to get random numbers, which in turn uses the rand package, which
	// is cryptographically safe.
	sessionKey, err := uuid.NewV4()
	if err != nil {
		logger.WithError(err).Error("Failed to generate a session key")

		return "", err
	}

	// 1 month
	keyDuration := time.Hour * 24 * 30

	// Do everything in a transaction so that we don't end up
	// with a corrupted state.
	err = s.Redis.Watch(ctx, func(tx *redis.Tx) error {
		// Create the session token's key
		err = s.Redis.Set(ctx, sessionRedisKey(sessionKey.String()), userId, keyDuration).Err()
		if err != nil {
			return err
		}

		// Add the session token to the user's session token inverted index. This inverted
		// index exists so that we can find all active sessions of a user and delete them.
		// Take a look at InvalidateSessions.
		err = s.Redis.SAdd(ctx, sessionInvertedIndexRedisKey(userId), sessionKey).Err()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		logger.WithError(err).Error("Failed to store session key: redis transaction failed")

		return "", err
	}

	return sessionKey.String(), nil
}

func (s *serviceImpl) InvalidateSessions(ctx context.Context, userId uint) error {
	logger := log.FromContext(ctx).WithField("userId", userId)

	logger.Debug("Invalidating all sessions of user")

	var sessionsSet []string

	// Get all session tokens of the user by getting the user's
	// sessions inverted index. It's basically a set that contains
	// all of the user's sessions.
	redisKey := sessionInvertedIndexRedisKey(userId)
	err := s.Redis.GetSet(ctx, redisKey, &sessionsSet).Err()
	if err != nil {
		logger.WithError(err).Error("Failed to get all of a user's session tokens")
		return err
	}

	// Convert the session tokens (which are just UUIDs) into
	// their respective redis keys so that we can delete them.
	// E.g.: convert
	//  "2c816d07-9499-4907-8ea3-1785dfa0f9a0"
	// into
	//  "session:2c816d07-9499-4907-8ea3-1785dfa0f9a0:user.id"
	for i, key := range sessionsSet {
		sessionsSet[i] = sessionRedisKey(key)
	}

	// Delete the session token keys and the user's
	// sessions inverted index.
	keysToDelete := append([]string{}, sessionsSet...)
	keysToDelete = append(keysToDelete, redisKey)

	err = s.Redis.Del(ctx, keysToDelete...).Err()
	if err != nil {
		logger.WithError(err).Error("Failed to delete session token keys")
		return err
	}

	return nil
}

// Maps a session key to a user id.
func sessionRedisKey(sessionKey string) string {
	return fmt.Sprintf("session:%s:user.id", sessionKey)
}

// Maps a user id to a set of session keys.
//
// It's an inverted index of sessionRedisKey.
func sessionInvertedIndexRedisKey(userId uint) string {
	return fmt.Sprintf("user:%d:session.keys", userId)
}
