package service

import (
	"fmt"

	"github.com/nishantd01/penguin-core/db"
)

type UserService struct{}

func NewUserService() *UserService {
	return &UserService{}
}

func (s *UserService) GetUser(id int) (*db.User, error) {
	// Business logic example: log id requested
	fmt.Printf("Requesting user with ID %d\n", id)
	return db.GetUserByID(id)
}
