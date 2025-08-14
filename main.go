package main

import (
	"database/sql"
	"fmt"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	v1 "github.com/nishantd01/penguin-core/controllers/v1"
	"github.com/nishantd01/penguin-core/service"
)

const (
	host     = "localhost"
	port     = 5432
	user     = "postgres_user"
	password = "qwerty"
	dbname   = "penguin"
)

func main() {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		panic(err)
	}
	defer db.Close()

	err = db.Ping()
	if err != nil {
		panic(err)
	}

	fmt.Println("Successfully connected")

	r := gin.Default()

	userService := service.NewUserService(db)
	userController := v1.NewUserController(userService)

	v1Group := r.Group("/api/v1")
	{
		v1Group.GET("/users/:id", userController.GetUser)
		v1Group.GET("/dbnames", userController.GetDbNames)
		v1Group.GET("/roles", userController.GetRoles)
		v1Group.POST("/check-edit-permission", userController.CheckAccess)
	}

	r.Run(":8083")
}
