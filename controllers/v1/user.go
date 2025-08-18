package v1

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nishantd01/penguin-core/models"
	"github.com/nishantd01/penguin-core/service"
)

type UserController struct {
	userService *service.UserService
}

func NewUserController(userService *service.UserService) *UserController {
	return &UserController{userService: userService}
}

// GET /v1/users/:id
func (ctl *UserController) GetUser(c *gin.Context) {
	idParam := c.Param("id")
	id, err := strconv.Atoi(idParam)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	user, err := ctl.userService.GetUser(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, user)
}

// GET /v1/dbnames
func (ctl *UserController) GetDbNames(ctx *gin.Context) {
	dbNames, err := ctl.userService.GetDbNames()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"db_name": dbNames,
		"count":   len(dbNames),
	})
}

// GET /v1/roles
func (ctl *UserController) GetRoles(ctx *gin.Context) {
	roleNames, err := ctl.userService.GetRoles()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusOK, gin.H{
		"roles": roleNames,
	})
}

func (c *UserController) CheckAccess(ctx *gin.Context) {
	var req service.AccessCheckRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body"})
		return
	}

	hasAccess, err := c.userService.CheckAccess(req)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if !hasAccess {
		ctx.JSON(http.StatusForbidden, gin.H{"message": "Access denied"})
		return
	}

	ctx.JSON(http.StatusOK, gin.H{"message": "Access granted"})
}

func (ctl *UserController) CreateReport(ctx *gin.Context) {
	var report models.ReportInput
	if err := ctx.ShouldBindJSON(&report); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fmt.Printf("req %v\n", report)

	code, msg := ctl.userService.CreateReport(report)

	ctx.JSON(code, gin.H{"message": msg})

}
