package main

import (
	"github.com/nishantd01/penguin-core/service"

	v1 "github.com/nishantd01/penguin-core/controllers/v1"

	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()

	userService := service.NewUserService()
	userController := v1.NewUserController(userService)

	v1Group := r.Group("/v1")
	{
		v1Group.GET("/users/:id", userController.GetUser)
	}

	r.Run(":8080")
}
