package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/nishantd01/penguin-core/db"
	"github.com/nishantd01/penguin-core/models"
	"github.com/nishantd01/penguin-core/utils"
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

type RoleMeta struct {
	Id   string `json:"id"`
	Name string `json:"name"`
}

func (s *UserService) GetRoles() ([]RoleMeta, error) {
	rows, err := s.db.Query("SELECT id,name FROM penguin.role")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var roleNames []RoleMeta
	for rows.Next() {
		var roleName RoleMeta
		if err := rows.Scan(&roleName.Id, &roleName.Name); err != nil {
			return nil, err
		}
		roleNames = append(roleNames, roleName)
	}
	return roleNames, rows.Err()
}

type AccessCheckRequest struct {
	Email      string `json:"email"`
	SheetId    string `json:"sheet_id"`
	ColumnName string `json:"column_name"`
}

// take sheetId as well , match permission wiyh reportId, email & column names rather thamn report name
func (s *UserService) CheckAccess(req AccessCheckRequest) (bool, error) {
	query := `
    SELECT EXISTS (
        SELECT 1
        FROM penguin.user u
        JOIN penguin.role r ON u.role_id = r.id
        JOIN penguin.spreadsheetpermissions sp ON sp.role_id = r.id
        JOIN penguin.spreadsheet s ON s.id = sp.spreadsheet_id
        WHERE u.email = $1
        AND s.id = $2
        AND $3 = ANY(sp.columns_permissions::text[])
    )
`

	var hasAccess bool
	err := s.db.QueryRow(query, req.Email, req.SheetId, req.ColumnName).Scan(&hasAccess)
	if err != nil {
		return false, fmt.Errorf("error checking access: %v", err)
	}

	return hasAccess, nil
}

func (s *UserService) CreateReport(req models.ReportInput) (int, string, string) {
	const scriptTitle = "BoundScriptForKshitiz"

	// Step 1: Create spreadsheet
	sheetId, err := utils.UploadSheet(req.ReportName, scriptTitle)
	if err != nil {
		log.Printf("Error creating spreadsheet: %v", err)
		return http.StatusInternalServerError, "Could not create spreadsheet, please try again", ""
	}

	// err = utils.AddSheetToSpreadsheet(sheetId, req.ReportName)
	// if err != nil {

	// 	log.Printf("Error AddSheetToSpreadsheet spreadsheet: %v", err)
	// 	return http.StatusInternalServerError, "Could not create AddSheetToSpreadsheet, please try again"
	// }

	// Step 5: Prepare sheet data
	sheetData, err := prepareData(s.db, req.SqlScript, req.Columns)
	if err != nil {
		log.Printf("Error preparing sheet data: %v", err)
		return http.StatusInternalServerError, "Failed to prepare data for sheet", ""
	}

	fmt.Printf("shetid %v\n", sheetId)

	// Step 6: Write data to the sheet
	err = utils.WriteDataToSheet(sheetId, "Sheet1", "A1", sheetData)
	if err != nil {
		log.Printf("Error writing data to sheet: %v", err)
		return http.StatusInternalServerError, "Failed to write data to sheet", ""
	}

	// 5. Now protect the header row (first row)
	err = utils.ProtectHeaderRow(sheetId, "Sheet1", sheetData)
	if err != nil {
		log.Printf("⚠️ Failed to protect header row: %v", err)
	} else {
		log.Println("✅ Header row protected")
	}

	// Convert columns to a map[string]string for schema
	schema := make(map[string]string)
	for _, col := range req.Columns {
		schema[col.Name] = col.DataType
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		log.Fatalf("Failed to marshal schema JSON: %v", err)
	}

	createdAt := time.Now()

	_, err = s.db.Exec(`
	INSERT INTO penguin.spreadsheet (id, report_name, created_at, schema)
	VALUES ($1, $2, $3, $4)
`, sheetId, req.ReportName, createdAt, schemaJSON)
	if err != nil {
		log.Fatalf("Failed to insert spreadsheet: %v", err)
	}

	// Step 2: Prepare permissions map
	result := make(map[string][]string)
	for _, col := range req.Columns {
		for _, role := range col.WritableBy {
			result[role] = append(result[role], col.Name)
		}
	}

	// Step 3: Prepare SQL insert statement
	ctx := context.Background()
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO penguin.spreadsheetpermissions (id,spreadsheet_id, role_id, columns_permissions)
		VALUES ($1, $2, $3,$4)
	`)
	if err != nil {
		log.Printf("Error preparing insert statement: %v", err)
		return http.StatusInternalServerError, "Internal server error", ""
	}
	defer stmt.Close()

	// Step 4: Insert permissions
	for roleIDStr, columns := range result {
		columnsJSON, err := json.Marshal(columns)
		if err != nil {
			log.Printf("Error marshaling columns for role %s: %v", roleIDStr, err)
			return http.StatusInternalServerError, "Internal server error", ""
		}

		fmt.Printf("roleIds %v\n", roleIDStr)

		uuidStr := uuid.New()
		_, err = stmt.ExecContext(ctx, uuidStr, sheetId, roleIDStr, string(columnsJSON))
		if err != nil {
			log.Printf("Insert failed for role %s: %v", roleIDStr, err)
			return http.StatusInternalServerError, "Internal server error", ""
		}
	}

	// Success log
	log.Printf("✅ Report created successfully with spreadsheet ID: %s", sheetId)
	return http.StatusOK, "Report created successfully", "https://docs.google.com/spreadsheets/d/" + sheetId
}

func prepareData(db *sql.DB, script string, newCols []models.Column) ([][]interface{}, error) {
	var query string
	if script == "" {
		query = "SELECT * FROM dummy LIMIT 100"
	} else {
		query = script
	}

	fmt.Printf("query: %v\n", query)

	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Get original DB column names
	dbCols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Create full column list with additional columns
	allCols := append([]string{}, dbCols...)
	for _, col := range newCols {
		allCols = append(allCols, col.Name)
	}

	// Initialize the data slice with header row
	header := make([]interface{}, len(allCols))
	for i, colName := range allCols {
		header[i] = colName
	}
	data := [][]interface{}{header}

	// Read DB rows
	for rows.Next() {
		rowData := make([]interface{}, len(dbCols))
		rowPtrs := make([]interface{}, len(dbCols))
		for i := range rowData {
			rowPtrs[i] = &rowData[i]
		}

		if err := rows.Scan(rowPtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Build final row with DB data + empty cells for newCols
		fullRow := make([]interface{}, len(allCols))
		copy(fullRow, rowData)

		// Fill extra columns with empty string or default
		for i := range newCols {
			fullRow[len(dbCols)+i] = "" // or newCols[i].DefaultValue if available
		}

		data = append(data, fullRow)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return data, nil
}
