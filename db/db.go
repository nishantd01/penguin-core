package db

import "errors"

type User struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

var users = []User{
	{ID: 1, Name: "Alice"},
	{ID: 2, Name: "Bob"},
}

func GetUserByID(id int) (*User, error) {
	for _, u := range users {
		if u.ID == id {
			return &u, nil
		}
	}
	return nil, errors.New("user not found")
}
