package main

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/gin-contrib/cors"
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

	fmt.Printf("password %v\n", password)

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

	r.Use(cors.Default())

	// OR Custom configuration
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:9000", "https://yourdomain.com"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	userService := service.NewUserService(db)
	userController := v1.NewUserController(userService)

	v1Group := r.Group("/api/v1")
	{
		// v1Group.GET("/users/:id", userController.GetUser)
		v1Group.GET("/dbnames", userController.GetDbNames)
		v1Group.GET("/roles", userController.GetRoles)
		v1Group.POST("/check-edit-permission", userController.CheckAccess)
		v1Group.POST("/create-report", userController.CreateReport)
		v1Group.POST("/validate-sql-query", userController.ValidateSQLQuery)
	}

	r.Run(":8083")
}

//
