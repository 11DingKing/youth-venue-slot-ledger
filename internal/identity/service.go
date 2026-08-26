package identity

import (
	"context"
	"fmt"
	"time"

	"github.com/11DingKing/youth-venue-slot-ledger/internal/auth"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/domain"
	"github.com/11DingKing/youth-venue-slot-ledger/internal/repository"
)

type Service struct {
	store *repository.Store
	now   func() time.Time
}

func New(store *repository.Store, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, now: now}
}

type CreateUserRequest struct {
	Email         string      `json:"email"`
	Password      string      `json:"password"`
	Name          string      `json:"name"`
	Role          domain.Role `json:"role"`
	BirthDate     *time.Time  `json:"birth_date,omitempty"`
	AbilityLevel  int         `json:"ability_level"`
	Qualification string      `json:"qualification,omitempty"`
}

func (s *Service) CreateUser(ctx context.Context, actor domain.Actor, request CreateUserRequest) (domain.User, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return domain.User{}, err
	}
	if !request.Role.Valid() || request.Name == "" || request.Email == "" {
		return domain.User{}, fmt.Errorf("%w: name, email and valid role required", domain.ErrValidation)
	}
	if request.Role == domain.RoleStudent && request.BirthDate == nil {
		return domain.User{}, fmt.Errorf("%w: student birth date required", domain.ErrValidation)
	}
	if request.Role == domain.RoleCoach && request.Qualification == "" {
		return domain.User{}, fmt.Errorf("%w: coach qualification required", domain.ErrValidation)
	}
	passwordHash, err := auth.HashPassword(request.Password)
	if err != nil {
		return domain.User{}, err
	}
	now := s.now().UTC()
	var created domain.User
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		var err error
		created, err = tx.CreateUser(ctx, domain.User{Email: request.Email, PasswordHash: passwordHash,
			Name: request.Name, Role: request.Role, BirthDate: request.BirthDate, AbilityLevel: request.AbilityLevel,
			Active: true, CreatedAt: now})
		if err != nil {
			return err
		}
		if request.Role == domain.RoleCoach {
			if _, err := tx.CreateCoach(ctx, created.ID, request.Qualification); err != nil {
				return err
			}
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "user.create", ObjectType: "user", ObjectID: repository.AuditObjectID(created.ID),
			Result: "success", RequestID: actor.RequestID, Metadata: map[string]string{"role": string(created.Role)}, CreatedAt: now})
		return err
	})
	return created, err
}

func (s *Service) AuthorizeGuardian(ctx context.Context, actor domain.Actor, authorization domain.GuardianAuthorization) (domain.GuardianAuthorization, error) {
	if err := actor.Require(domain.RoleOperator); err != nil {
		return domain.GuardianAuthorization{}, err
	}
	now := s.now().UTC()
	var created domain.GuardianAuthorization
	err := s.store.WithTx(ctx, func(tx *repository.Store) error {
		guardian, err := tx.UserByID(ctx, authorization.GuardianID)
		if err != nil {
			return err
		}
		student, err := tx.UserByID(ctx, authorization.StudentID)
		if err != nil {
			return err
		}
		if guardian.Role != domain.RoleGuardian || student.Role != domain.RoleStudent {
			return fmt.Errorf("%w: guardian/student roles do not match", domain.ErrValidation)
		}
		created, err = tx.CreateGuardianAuthorization(ctx, authorization)
		if err != nil {
			return err
		}
		_, err = tx.AppendAudit(ctx, domain.AuditEvent{ActorID: actor.UserID, ActorRole: actor.Role,
			Action: "guardian.authorize", ObjectType: "guardian_authorization", ObjectID: repository.AuditObjectID(created.ID),
			Result: "success", RequestID: actor.RequestID, Metadata: map[string]string{
				"guardian_id": repository.AuditObjectID(guardian.ID), "student_id": repository.AuditObjectID(student.ID),
			}, CreatedAt: now})
		return err
	})
	return created, err
}
