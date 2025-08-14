package service

import (
	"database/sql"
	"fmt"

	"github.com/nishantd01/penguin-core/db"
)

type UserService struct {
	db *sql.DB
}

func NewUserService(db *sql.DB) *UserService {
	return &UserService{db: db}
}

func (s *UserService) GetUser(id int) (*db.User, error) {
	fmt.Printf("Requesting user with ID %d\n", id)
	return db.GetUserByID(id)
}

func (s *UserService) GetDbNames() ([]string, error) {
	rows, err := s.db.Query("SELECT database_name FROM penguin.snowflake_databases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var dbNames []string
	for rows.Next() {
		var dbName string
		if err := rows.Scan(&dbName); err != nil {
			return nil, err
		}
		dbNames = append(dbNames, dbName)
	}
	return dbNames, rows.Err()
}

func (s *UserService) GetRoles() ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT (name) FROM penguin.role")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roleNames []string
	for rows.Next() {
		var roleName string
		if err := rows.Scan(&roleName); err != nil {
			return nil, err
		}
		roleNames = append(roleNames, roleName)
	}
	return roleNames, rows.Err()
}

type AccessCheckRequest struct {
	Email      string `json:"email"`
	ReportName string `json:"report_name"`
	ColumnName string `json:"column_name"`
}

func (s *UserService) CheckAccess(req AccessCheckRequest) (bool, error) {
	query := `
        SELECT EXISTS (
            SELECT 1
            FROM penguin.user u
            JOIN penguin.role r ON u.role_id = r.id
            JOIN penguin.spreadsheetpermissions sp ON sp.role_id = r.id
            JOIN penguin.spreadsheet s ON s.id = sp.spreadsheet_id
            WHERE u.email = $1
            AND s.report_name = $2
            AND sp.columns_permissions ILIKE '%' || $3 || '%'
        )
    `

	var hasAccess bool
	err := s.db.QueryRow(query, req.Email, req.ReportName, req.ColumnName).Scan(&hasAccess)
	if err != nil {
		return false, fmt.Errorf("error checking access: %v", err)
	}

	return hasAccess, nil
}
