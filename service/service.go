package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/nishantd01/penguin-core/db"
	"github.com/nishantd01/penguin-core/models"
	"github.com/nishantd01/penguin-core/utils"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/option"
	"google.golang.org/api/script/v1"
	"google.golang.org/api/sheets/v4"
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
        SELECT sp.columns_permissions
        FROM penguin.user u
        JOIN penguin.role r ON u.role_id = r.id
        JOIN penguin.spreadsheetpermissions sp ON sp.role_id = r.id
        JOIN penguin.spreadsheet s ON s.id = sp.spreadsheet_id
        WHERE u.email = $1
        AND s.id = $2
    `

	var columnsAllowed string
	err := s.db.QueryRow(query, req.Email, req.SheetId).Scan(&columnsAllowed)
	if err != nil {
		log.Printf("Error decoding columns_permissions:", err)
		return false, err
	}

	var columns []string
	err = json.Unmarshal([]byte(columnsAllowed), &columns)
	if err != nil {
		log.Printf("Error decoding each column:", err)
		return false, err
	}

	return contains(columns, req.ColumnName), nil

	// return false, nil
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

		fmt.Printf("columnJson %v\n", columnsJSON)

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

// Reads the OAuth token from a file
func tokenFromFile(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("Warning: failed to close file: %v", closeErr)
		}
	}()
	tok := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(tok)
	return tok, err
}

func getClient(config *oauth2.Config) *http.Client {
	tok, err := tokenFromFile("token.json")
	if err != nil {
		log.Fatalf("Token not found. Run the authorization code flow first. Error: %v", err)
	}
	return config.Client(context.Background(), tok)
}

func (s *UserService) CreateStagedReport(req models.ReportInput) (int, string, string) {
	ctx := context.Background()
	b, err := os.ReadFile("client_secret.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	// Get OAuth2 config with additional scopes for Apps Script
	config, err := google.ConfigFromJSON(b,
		sheets.SpreadsheetsScope,
		script.ScriptProjectsScope,
		"https://www.googleapis.com/auth/drive",
		"https://www.googleapis.com/auth/script.projects",
		"https://www.googleapis.com/auth/script.deployments",
	)
	if err != nil {
		return http.StatusInternalServerError, "Internal server error", ""
	}

	client := getClient(config)

	// Initialize Sheets service
	srv, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Unable to retrieve Sheets client: %v", err)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	// Initialize Apps Script service
	scriptSrv, err := script.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Unable to create Apps Script service: %v", err)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	// Create sheets array based on stages
	var sheetsList []*sheets.Sheet

	// First sheet with data
	sheetsList = append(sheetsList, &sheets.Sheet{
		Properties: &sheets.SheetProperties{
			Title:   req.Stages[0].Name,
			SheetId: 0,
		},
	})

	// Additional sheets for remaining stages (empty with headers only)
	for i, stage := range req.Stages[1:] {
		sheetsList = append(sheetsList, &sheets.Sheet{
			Properties: &sheets.SheetProperties{
				Title:   stage.Name,
				SheetId: int64(i + 1),
			},
		})
	}

	// Create spreadsheet with dynamic tabs based on stages
	spreadsheet := &sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{
			Title: req.ReportName,
		},
		Sheets: sheetsList,
	}

	// Create the spreadsheet
	resp, err := srv.Spreadsheets.Create(spreadsheet).Do()
	if err != nil {
		log.Printf("Error creating spreadsheet: %v", err)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	fmt.Printf("✅ Spreadsheet created with %d tabs!\n", len(req.Stages))
	fmt.Printf("📄 ID: %s\n", resp.SpreadsheetId)
	fmt.Printf("🔗 URL: https://docs.google.com/spreadsheets/d/%s\n", resp.SpreadsheetId)

	// Prepare data from SQL query for the first tab
	sheetData, err := prepareData(s.db, req.SqlScript, req.Columns)
	if err != nil {
		log.Printf("Error preparing sheet data: %v", err)
		return http.StatusInternalServerError, "Failed to prepare data for sheet", ""
	}

	// Add data to the first tab
	firstTabRange := fmt.Sprintf("'%s'!A1", req.Stages[0].Name)
	firstTabData := &sheets.ValueRange{
		Values: sheetData,
	}

	_, err = srv.Spreadsheets.Values.Update(resp.SpreadsheetId, firstTabRange, firstTabData).
		ValueInputOption("RAW").Do()
	if err != nil {
		log.Printf("Error writing data to first tab: %v", err)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	// Add headers to remaining tabs (empty with headers only)
	if len(sheetData) > 0 {
		headerRow := [][]interface{}{sheetData[0]} // First row contains headers

		for i := 1; i < len(req.Stages); i++ {
			stage := req.Stages[i]
			headerRange := fmt.Sprintf("'%s'!A1", stage.Name)
			headerData := &sheets.ValueRange{
				Values: headerRow,
			}

			_, err = srv.Spreadsheets.Values.Update(resp.SpreadsheetId, headerRange, headerData).
				ValueInputOption("RAW").Do()
			if err != nil {
				log.Printf("Unable to write headers to tab %s: %v", stage.Name, err)
			} else {
				fmt.Printf("✅ Headers added to tab: %s\n", stage.Name)
			}
		}
	}

	// Create Apps Script project and bind it to the spreadsheet
	scriptProject := &script.CreateProjectRequest{
		Title:    "Staged Workflow Automation Script",
		ParentId: resp.SpreadsheetId,
	}

	scriptResp, err := scriptSrv.Projects.Create(scriptProject).Do()
	if err != nil {
		if strings.Contains(fmt.Sprintf("%v", err), "Apps Script API has not been used") {
			fmt.Printf("⚠️  Apps Script API needs to be enabled\n")
			fmt.Printf("🔗 Please visit Google Cloud Console to enable Apps Script API\n")
			fmt.Printf("📋 Click 'ENABLE' button and wait 2-3 minutes, then run the program again\n")
		} else {
			log.Printf("Unable to create Apps Script project: %v", err)
		}
	} else {
		fmt.Printf("🔧 Apps Script project created: %s\n", scriptResp.ScriptId)

		// Generate dynamic Apps Script code based on stages
		scriptContent := generateStagedWorkflowScript(req.Stages)

		// Update the Apps Script project with the code
		files := []*script.File{
			{
				Name: "appsscript",
				Type: "JSON",
				Source: `{
  "timeZone": "America/New_York",
  "dependencies": {
    "enabledAdvancedServices": []
  },
  "exceptionLogging": "STACKDRIVER",
  "runtimeVersion": "V8"
}`,
			},
			{
				Name:   "Code",
				Type:   "SERVER_JS",
				Source: scriptContent,
			},
		}

		content := &script.Content{
			Files: files,
		}

		_, err = scriptSrv.Projects.UpdateContent(scriptResp.ScriptId, content).Do()
		if err != nil {
			log.Printf("Unable to update Apps Script content: %v", err)
		} else {
			fmt.Printf("📝 Dynamic staged workflow Apps Script code injected successfully!\n")
			fmt.Printf("🔗 Apps Script URL: https://script.google.com/d/%s/edit\n", scriptResp.ScriptId)
		}
	}

	// Save to database
	err = s.saveStagedReportToDatabase(resp.SpreadsheetId, req)
	if err != nil {
		log.Printf("Warning: Failed to save report to database: %v", err)
	}

	// Save stages to database
	err = s.saveStagesDataToDatabase(resp.SpreadsheetId, req.Stages)
	if err != nil {
		log.Printf("Warning: Failed to save stages data to database: %v", err)
	}

	fmt.Printf("\n🎉 Setup complete! Your staged workflow system includes:\n")
	fmt.Printf("   • %d workflow stages with dynamic tabs\n", len(req.Stages))
	fmt.Printf("   • First tab '%s' populated with %d rows of data\n", req.Stages[0].Name, len(sheetData)-1)
	fmt.Printf("   • Remaining tabs with headers ready for workflow\n")
	fmt.Printf("   • Automatic workflow movement and professional formatting\n")

	url := "https://docs.google.com/spreadsheets/d/" + resp.SpreadsheetId
	return http.StatusOK, "Staged Workflow Spreadsheet Created", url
}

func (s *UserService) saveStagedReportToDatabase(spreadsheetId string, req models.ReportInput) error {
	// Convert columns to a map[string]string for schema
	schema := make(map[string]string)
	for _, col := range req.Columns {
		schema[col.Name] = col.DataType
	}

	schemaJSON, err := json.Marshal(schema)
	if err != nil {
		return fmt.Errorf("failed to marshal schema JSON: %w", err)
	}

	createdAt := time.Now()

	// Insert spreadsheet record
	_, err = s.db.Exec(`
		INSERT INTO penguin.spreadsheet (id, report_name, created_at, schema)
		VALUES ($1, $2, $3, $4)
	`, spreadsheetId, req.ReportName, createdAt, schemaJSON)
	if err != nil {
		return fmt.Errorf("failed to insert spreadsheet: %w", err)
	}

	// For staged reports, we'll create permissions based on stage roles
	// Each stage gets permissions for all columns
	ctx := context.Background()
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO penguin.spreadsheetpermissions (id, spreadsheet_id, role_id, columns_permissions)
		VALUES ($1, $2, $3, $4)
	`)
	if err != nil {
		return fmt.Errorf("error preparing insert statement: %w", err)
	}
	defer stmt.Close()

	// Create column names list for permissions
	columnNames := make([]string, len(req.Columns))
	for i, col := range req.Columns {
		columnNames[i] = col.Name
	}
	columnsJSON, err := json.Marshal(columnNames)
	if err != nil {
		return fmt.Errorf("error marshaling column names: %w", err)
	}

	// Insert permissions for each role in each stage
	insertedRoles := make(map[string]bool) // To avoid duplicates
	for _, stage := range req.Stages {
		for _, roleID := range stage.Roles {
			if !insertedRoles[roleID] {
				uuidStr := uuid.New()
				_, err = stmt.ExecContext(ctx, uuidStr, spreadsheetId, roleID, string(columnsJSON))
				if err != nil {
					return fmt.Errorf("insert failed for role %s: %w", roleID, err)
				}
				insertedRoles[roleID] = true
			}
		}
	}

	log.Printf("✅ Staged report saved to database with spreadsheet ID: %s", spreadsheetId)
	return nil
}

func (s *UserService) saveStagesDataToDatabase(spreadsheetId string, stages []models.Stage) error {
	ctx := context.Background()
	stmt, err := s.db.PrepareContext(ctx, `
		INSERT INTO penguin.stages (id, spreadsheet_id, name, description, roles, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`)
	if err != nil {
		return fmt.Errorf("error preparing insert statement for stages: %w", err)
	}
	defer stmt.Close()

	now := time.Now()
	for _, stage := range stages {
		stageId := uuid.New()

		// Convert roles slice to PostgreSQL UUID array format
		rolesArray := make([]string, len(stage.Roles))
		for i, roleId := range stage.Roles {
			rolesArray[i] = roleId
		}

		// Use PostgreSQL array syntax
		rolesArrayStr := "{" + strings.Join(rolesArray, ",") + "}"

		_, err = stmt.ExecContext(ctx, stageId, spreadsheetId, stage.Name, stage.Description, rolesArrayStr, now, now)
		if err != nil {
			return fmt.Errorf("failed to insert stage %s: %w", stage.Name, err)
		}
	}

	log.Printf("✅ Stages data saved to database for spreadsheet ID: %s", spreadsheetId)
	return nil
}

func contains(slice []string, str string) bool {
	for _, s := range slice {
		if s == str {
			return true
		}
	}
	return false
}

func prepareData(db *sql.DB, script string, newCols []models.Column) ([][]interface{}, error) {

	// sc

	fmt.Printf("query: %v\n", script)

	// asihfahisofhiboashbfoahbsobfcaoiswbf
	// select * from penguin.dev_logs limit 100 -> // select * from penguin.dev_logs limit 1

	rows, err := db.Query(script)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	// Get original DB column names
	dbCols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	fmt.Printf("there %v\n", newCols)

	// Create full column list with additional columns
	allCols := append([]string{}, dbCols...)
	var nCols []models.Column
	for _, col := range newCols {
		if !contains(allCols, col.Name) {
			allCols = append(allCols, col.Name)
			nCols = append(nCols, col)
		}
	}

	fmt.Printf("Here %v\n", allCols)

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
		for i := range nCols {
			fullRow[len(dbCols)+i] = "" // or newCols[i].DefaultValue if available
		}

		data = append(data, fullRow)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	return data, nil
}

type SQLValidationRequest struct {
	Query  string `json:"query"`
	DBName string `json:"db_name"`
}

type ColumnInfo struct {
	ColName  string `json:"col_name"`
	DataType string `json:"data_type"`
}

type SQLValidationResponse struct {
	Columns []ColumnInfo `json:"columns"`
	Count   int          `json:"count"`
}

func (s *UserService) ValidateSQLQuery(req SQLValidationRequest) (*SQLValidationResponse, error) {
	// First switch to the requested database
	_, err := s.db.Exec(fmt.Sprintf("SET search_path TO %s", req.DBName))
	if err != nil {
		return nil, fmt.Errorf("failed to switch database: %v", err)
	}

	// Clean the query by trimming spaces and removing trailing semicolons
	query := strings.TrimSpace(req.Query)
	query = strings.TrimRight(query, ";")

	// Try to prepare the statement to validate SQL syntax
	stmt, err := s.db.Prepare(query)
	if err != nil {
		return nil, fmt.Errorf("invalid SQL query: %v", err)
	}
	defer stmt.Close()

	// Execute the query with LIMIT 0 to get column information without fetching data
	rows, err := s.db.Query(query + " LIMIT 0")
	if err != nil {
		return nil, fmt.Errorf("error executing query: %v", err)
	}
	defer rows.Close()

	// Get column types
	columnTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("error getting column types: %v", err)
	}

	// Build column info
	columns := make([]ColumnInfo, len(columnTypes))
	for i, col := range columnTypes {
		columns[i] = ColumnInfo{
			ColName:  col.Name(),
			DataType: col.DatabaseTypeName(),
		}
	}

	// Get total count
	countQuery := fmt.Sprintf("SELECT COUNT(*) FROM (%s) as sub", query)
	var count int
	err = s.db.QueryRow(countQuery).Scan(&count)
	if err != nil {
		return nil, fmt.Errorf("error getting row count: %v", err)
	}

	return &SQLValidationResponse{
		Columns: columns,
		Count:   count,
	}, nil
}

func generateStagedWorkflowScript(stages []models.Stage) string {
	// Generate stage names array for JavaScript
	stageNames := make([]string, len(stages))
	for i, stage := range stages {
		stageNames[i] = fmt.Sprintf("'%s'", stage.Name)
	}
	stageNamesJS := strings.Join(stageNames, ", ")

	scriptContent := fmt.Sprintf(`
function onOpen() {
  var ui = SpreadsheetApp.getUi();
  ui.createMenu('Workflow Tools')
    .addItem('Setup Workflow System', 'setupWorkflowSystem')
    .addItem('Refresh Workflow', 'refreshWorkflow')
    .addItem('Clear All Data', 'clearAllData')
    .addToUi();
    
  // Auto-setup workflow when spreadsheet opens
  setupWorkflowSystem();
}

var STAGE_NAMES = [%s];

// Global variables to handle race conditions and prevent data loss
var isProcessingWorkflow = false;
var workflowQueue = [];
var processingRows = {}; // Track rows being processed to prevent duplicates
var rowProcessingLock = {}; // Additional lock per sheet to prevent concurrent processing

function setupWorkflowSystem() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  
  // Setup workflow for all stages
  for (var i = 0; i < STAGE_NAMES.length; i++) {
    setupStageWorkflow(STAGE_NAMES[i], i);
  }
  
  // Format all sheets
  formatAllSheets();
  
  SpreadsheetApp.getUi().alert('Workflow system has been set up successfully!');
}

function setupStageWorkflow(stageName, stageIndex) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var sheet = ss.getSheetByName(stageName);
  
  if (!sheet) return;
  
  var lastRow = sheet.getLastRow();
  if (lastRow <= 1) return; // No data rows
  
  // Find existing workflow columns or create new ones
  var forwardCol = findOrCreateForwardWorkflowColumn(sheet, stageIndex);
  var backwardCol = findOrCreateBackwardWorkflowColumn(sheet, stageIndex);
  
  // Add checkboxes for each data row
  for (var i = 2; i <= lastRow; i++) {
    // Forward movement checkbox
    var forwardCheckboxCell = sheet.getRange(i, forwardCol);
    if (!forwardCheckboxCell.isChecked() && forwardCheckboxCell.getValue() !== true && forwardCheckboxCell.getValue() !== false) {
      forwardCheckboxCell.insertCheckboxes();
      forwardCheckboxCell.setValue(false);
    }
    
    // Backward movement checkbox (only if not first stage)
    if (backwardCol !== -1) {
      var backwardCheckboxCell = sheet.getRange(i, backwardCol);
      if (!backwardCheckboxCell.isChecked() && backwardCheckboxCell.getValue() !== true && backwardCheckboxCell.getValue() !== false) {
        backwardCheckboxCell.insertCheckboxes();
        backwardCheckboxCell.setValue(false);
      }
    }
  }
}

function findOrCreateForwardWorkflowColumn(sheet, stageIndex) {
  var lastCol = sheet.getLastColumn();
  
  // Check if forward workflow column already exists
  for (var col = 1; col <= lastCol; col++) {
    var headerValue = sheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move to') !== -1 || headerValue.indexOf('Complete') !== -1) {
      return col; // Return existing forward workflow column
    }
  }
  
  // Create new forward workflow column
  var workflowCol = lastCol + 1;
  if (stageIndex < STAGE_NAMES.length - 1) {
    sheet.getRange(1, workflowCol).setValue('Move to ' + STAGE_NAMES[stageIndex + 1]);
  } else {
    sheet.getRange(1, workflowCol).setValue('Complete');
  }
  
  return workflowCol;
}

function findOrCreateBackwardWorkflowColumn(sheet, stageIndex) {
  // First stage doesn't have a backward column
  if (stageIndex === 0) {
    return -1;
  }
  
  var lastCol = sheet.getLastColumn();
  
  // Check if backward workflow column already exists
  for (var col = 1; col <= lastCol; col++) {
    var headerValue = sheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move back to') !== -1) {
      return col; // Return existing backward workflow column
    }
  }
  
  // Create new backward workflow column
  var workflowCol = lastCol + 1;
  sheet.getRange(1, workflowCol).setValue('Move back to ' + STAGE_NAMES[stageIndex - 1]);
  
  return workflowCol;
}

function onEdit(e) {
  var range = e.range;
  var sheet = e.source.getActiveSheet();
  var row = range.getRow();
  var col = range.getColumn();
  var value = range.getValue();
  var sheetName = sheet.getName();
  
  // Check if this is a workflow checkbox click
  if (row > 1 && value === true) {
    // Check if this column is a workflow column
    var headerValue = sheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move to') !== -1 || headerValue.indexOf('Complete') !== -1) {
      queueWorkflowAction('forward', sheetName, row, col);
    } else if (headerValue.indexOf('Move back to') !== -1) {
      queueWorkflowAction('backward', sheetName, row, col);
    }
  }
}

function queueWorkflowAction(direction, sheetName, row, col) {
  // Create unique identifier for this specific row content
  var sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName(sheetName);
  if (!sheet || row > sheet.getLastRow()) {
    console.log('Sheet or row does not exist during queue action');
    return;
  }
  
  // Get the actual data for this row to create a unique signature
  var dataColumns = getDataColumnCount(sheet);
  var rowData;
  
  try {
    rowData = sheet.getRange(row, 1, 1, dataColumns).getValues()[0];
  } catch (error) {
    console.log('Error reading row data during queuing: ' + error.message);
    return;
  }
  
  // Create unique row signature based on content (not just position)
  var rowSignature = sheetName + '_' + JSON.stringify(rowData);
  
  // Check if this exact row content is already being processed
  if (processingRows[rowSignature]) {
    console.log('Row with same content in ' + sheetName + ' is already being processed. Skipping duplicate action.');
    // Reset the checkbox since we're ignoring this click
    try {
      sheet.getRange(row, col).setValue(false);
    } catch (resetError) {
      console.log('Error resetting checkbox: ' + resetError.message);
    }
    return;
  }
  
  // Mark this row signature as being processed
  processingRows[rowSignature] = true;
  
  // Add the action to queue with content-based identifier
  workflowQueue.push({
    direction: direction,
    sheetName: sheetName,
    row: row,
    col: col,
    rowSignature: rowSignature,
    rowData: rowData,
    timestamp: new Date().getTime()
  });
  
  // Process the queue if not already processing
  if (!isProcessingWorkflow) {
    processWorkflowQueue();
  }
}

function processWorkflowQueue() {
  if (workflowQueue.length === 0) {
    isProcessingWorkflow = false;
    return;
  }
  
  isProcessingWorkflow = true;
  
  // Process actions one by one with enhanced validation
  while (workflowQueue.length > 0) {
    var action = workflowQueue.shift();
    
    // Validate that the sheet exists
    var sheet = SpreadsheetApp.getActiveSpreadsheet().getSheetByName(action.sheetName);
    if (!sheet) {
      console.log('Sheet ' + action.sheetName + ' no longer exists');
      delete processingRows[action.rowSignature];
      continue;
    }
    
    // Find the row with matching content (in case row numbers shifted)
    var actualRow = findRowWithContent(sheet, action.rowData);
    if (actualRow === -1) {
      console.log('Row with content no longer exists in ' + action.sheetName);
      delete processingRows[action.rowSignature];
      continue;
    }
    
    // Validate checkbox is still checked at the correct position
    var checkboxCell = sheet.getRange(actualRow, action.col);
    if (checkboxCell.getValue() !== true) {
      console.log('Checkbox is no longer checked for row in ' + action.sheetName);
      delete processingRows[action.rowSignature];
      continue;
    }
    
    try {
      // Process the workflow action with the correct row number
      var success = false;
      if (action.direction === 'forward') {
        success = handleForwardWorkflowMove(action.sheetName, actualRow, action.rowData);
      } else if (action.direction === 'backward') {
        success = handleBackwardWorkflowMove(action.sheetName, actualRow, action.rowData);
      }
      
      // Clean up processing marker
      delete processingRows[action.rowSignature];
      
      // Small delay to prevent overwhelming the system
      Utilities.sleep(200);
      
    } catch (error) {
      console.log('Error processing workflow action: ' + error.message);
      // Clean up processing marker on error
      delete processingRows[action.rowSignature];
      
      // Reset the checkbox on error to prevent data loss
      try {
        checkboxCell.setValue(false);
      } catch (resetError) {
        console.log('Error resetting checkbox: ' + resetError.message);
      }
    }
  }
  
  isProcessingWorkflow = false;
}

function findRowWithContent(sheet, targetData) {
  var lastRow = sheet.getLastRow();
  var dataColumns = getDataColumnCount(sheet);
  
  if (lastRow <= 1) return -1;
  
  // Search for matching row content
  for (var row = 2; row <= lastRow; row++) {
    try {
      var rowData = sheet.getRange(row, 1, 1, dataColumns).getValues()[0];
      
      // Compare row content
      var matches = true;
      for (var col = 0; col < Math.min(targetData.length, rowData.length); col++) {
        if (String(targetData[col]) !== String(rowData[col])) {
          matches = false;
          break;
        }
      }
      
      if (matches) {
        return row;
      }
    } catch (error) {
      continue; // Skip problematic rows
    }
  }
  
  return -1; // Not found
}

function handleForwardWorkflowMove(currentStageName, row, originalRowData) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var currentSheet = ss.getSheetByName(currentStageName);
  
  if (!currentSheet) return false;
  
  // Double-check that the row still exists and has the expected content
  if (row > currentSheet.getLastRow()) {
    console.log('Row ' + row + ' no longer exists in ' + currentStageName);
    return false;
  }
  
  // Verify row content hasn't changed
  var dataColumns = getDataColumnCount(currentSheet);
  var currentRowData;
  try {
    currentRowData = currentSheet.getRange(row, 1, 1, dataColumns).getValues()[0];
  } catch (error) {
    console.log('Error reading current row data: ' + error.message);
    return false;
  }
  
  // Verify this is still the same row we intended to process
  var contentMatches = true;
  for (var i = 0; i < Math.min(originalRowData.length, currentRowData.length); i++) {
    if (String(originalRowData[i]) !== String(currentRowData[i])) {
      contentMatches = false;
      break;
    }
  }
  
  if (!contentMatches) {
    console.log('Row content has changed, skipping move operation');
    return false;
  }
  
  // Find current stage index
  var currentStageIndex = -1;
  for (var i = 0; i < STAGE_NAMES.length; i++) {
    if (STAGE_NAMES[i] === currentStageName) {
      currentStageIndex = i;
      break;
    }
  }
  
  if (currentStageIndex === -1) return false;
  
  // Find forward workflow column
  var forwardWorkflowCol = -1;
  var lastCol = currentSheet.getLastColumn();
  for (var col = 1; col <= lastCol; col++) {
    var headerValue = currentSheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move to') !== -1 || headerValue.indexOf('Complete') !== -1) {
      forwardWorkflowCol = col;
      break;
    }
  }
  
  if (forwardWorkflowCol === -1) return false;
  
  // Validate row has actual data
  if (!currentRowData[0] || currentRowData[0] === '') {
    currentSheet.getRange(row, forwardWorkflowCol).setValue(false);
    return false;
  }
  
  if (currentStageIndex < STAGE_NAMES.length - 1) {
    // Move to next stage
    var nextStageName = STAGE_NAMES[currentStageIndex + 1];
    var nextSheet = ss.getSheetByName(nextStageName);
    
    if (nextSheet) {
      try {
        // Lock processing for this sheet to prevent conflicts
        var lockKey = currentStageName + '_processing';
        if (rowProcessingLock[lockKey]) {
          console.log('Sheet ' + currentStageName + ' is locked for processing');
          return false;
        }
        rowProcessingLock[lockKey] = true;
        
        // Find or create workflow columns in next sheet
        var nextForwardCol = findOrCreateForwardWorkflowColumn(nextSheet, currentStageIndex + 1);
        var nextBackwardCol = findOrCreateBackwardWorkflowColumn(nextSheet, currentStageIndex + 1);
        
        // Add to next sheet first
        var nextLastRow = nextSheet.getLastRow();
        var newRow = nextLastRow + 1;
        
        // Insert the data (only data columns, not workflow columns)
        nextSheet.getRange(newRow, 1, 1, currentRowData.length).setValues([currentRowData]);
        
        // Add forward workflow checkbox
        var forwardCheckboxCell = nextSheet.getRange(newRow, nextForwardCol);
        forwardCheckboxCell.insertCheckboxes();
        forwardCheckboxCell.setValue(false);
        
        // Add backward workflow checkbox if applicable
        if (nextBackwardCol !== -1) {
          var backwardCheckboxCell = nextSheet.getRange(newRow, nextBackwardCol);
          backwardCheckboxCell.insertCheckboxes();
          backwardCheckboxCell.setValue(false);
        }
        
        // Only remove from current sheet after successfully adding to next sheet
        currentSheet.deleteRow(row);
        
        // Release lock
        delete rowProcessingLock[lockKey];
        
        console.log('Moved item forward from ' + currentStageName + ' to ' + nextStageName);
        return true;
        
      } catch (error) {
        // Release lock on error
        delete rowProcessingLock[lockKey];
        console.log('Error moving row forward: ' + error.message);
        
        // Reset checkbox to prevent data loss
        try {
          currentSheet.getRange(row, forwardWorkflowCol).setValue(false);
        } catch (resetError) {
          console.log('Error resetting checkbox: ' + resetError.message);
        }
        return false;
      }
    }
  } else {
    // Final stage - mark as complete
    try {
      // Keep the checkbox checked instead of resetting it
      // Apply lighter green background to completed row with better styling
      currentSheet.getRange(row, 1, 1, dataColumns).setBackground('#4CAF50'); // Lighter green
      currentSheet.getRange(row, 1, 1, dataColumns).setFontColor('#FFFFFF'); // White text for better contrast
      currentSheet.getRange(row, 1, 1, dataColumns).setFontWeight('bold'); // Make text bold
      
      // Optionally add a completion timestamp in an adjacent column if space allows
      var timestampCol = dataColumns + 1;
      if (timestampCol < forwardWorkflowCol) {
        var now = new Date();
        var timestamp = Utilities.formatDate(now, Session.getScriptTimeZone(), 'MM/dd/yyyy HH:mm:ss');
        currentSheet.getRange(row, timestampCol).setValue('Completed: ' + timestamp);
        currentSheet.getRange(row, timestampCol).setBackground('#4CAF50');
        currentSheet.getRange(row, timestampCol).setFontColor('#FFFFFF');
        currentSheet.getRange(row, timestampCol).setFontWeight('bold');
      }
      
      console.log('Item completed in final stage: ' + currentStageName);
      return true;
      
    } catch (error) {
      console.log('Error completing item: ' + error.message);
      // Reset checkbox on error
      try {
        currentSheet.getRange(row, forwardWorkflowCol).setValue(false);
      } catch (resetError) {
        console.log('Error resetting checkbox: ' + resetError.message);
      }
      return false;
    }
  }
  
  return false;
}

function handleBackwardWorkflowMove(currentStageName, row, originalRowData) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var currentSheet = ss.getSheetByName(currentStageName);
  
  if (!currentSheet) return false;
  
  // Double-check that the row still exists and has the expected content
  if (row > currentSheet.getLastRow()) {
    console.log('Row ' + row + ' no longer exists in ' + currentStageName);
    return false;
  }
  
  // Verify row content hasn't changed
  var dataColumns = getDataColumnCount(currentSheet);
  var currentRowData;
  try {
    currentRowData = currentSheet.getRange(row, 1, 1, dataColumns).getValues()[0];
  } catch (error) {
    console.log('Error reading current row data: ' + error.message);
    return false;
  }
  
  // Verify this is still the same row we intended to process
  var contentMatches = true;
  for (var i = 0; i < Math.min(originalRowData.length, currentRowData.length); i++) {
    if (String(originalRowData[i]) !== String(currentRowData[i])) {
      contentMatches = false;
      break;
    }
  }
  
  if (!contentMatches) {
    console.log('Row content has changed, skipping backward move operation');
    return false;
  }
  
  // Find current stage index
  var currentStageIndex = -1;
  for (var i = 0; i < STAGE_NAMES.length; i++) {
    if (STAGE_NAMES[i] === currentStageName) {
      currentStageIndex = i;
      break;
    }
  }
  
  if (currentStageIndex === -1 || currentStageIndex === 0) return false; // Can't move back from first stage
  
  // Find backward workflow column
  var backwardWorkflowCol = -1;
  var lastCol = currentSheet.getLastColumn();
  for (var col = 1; col <= lastCol; col++) {
    var headerValue = currentSheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move back to') !== -1) {
      backwardWorkflowCol = col;
      break;
    }
  }
  
  if (backwardWorkflowCol === -1) return false;
  
  // Validate row has actual data
  if (!currentRowData[0] || currentRowData[0] === '') {
    currentSheet.getRange(row, backwardWorkflowCol).setValue(false);
    return false;
  }
  
  // Move to previous stage
  var previousStageName = STAGE_NAMES[currentStageIndex - 1];
  var previousSheet = ss.getSheetByName(previousStageName);
  
  if (previousSheet) {
    try {
      // Lock processing for this sheet to prevent conflicts
      var lockKey = currentStageName + '_processing_backward';
      if (rowProcessingLock[lockKey]) {
        console.log('Sheet ' + currentStageName + ' is locked for backward processing');
        return false;
      }
      rowProcessingLock[lockKey] = true;
      
      // Find or create workflow columns in previous sheet
      var prevForwardCol = findOrCreateForwardWorkflowColumn(previousSheet, currentStageIndex - 1);
      var prevBackwardCol = findOrCreateBackwardWorkflowColumn(previousSheet, currentStageIndex - 1);
      
      // Add to previous sheet first
      var prevLastRow = previousSheet.getLastRow();
      var newRow = prevLastRow + 1;
      
      // Insert the data (only data columns, not workflow columns)
      previousSheet.getRange(newRow, 1, 1, currentRowData.length).setValues([currentRowData]);
      
      // Add forward workflow checkbox
      var forwardCheckboxCell = previousSheet.getRange(newRow, prevForwardCol);
      forwardCheckboxCell.insertCheckboxes();
      forwardCheckboxCell.setValue(false);
      
      // Add backward workflow checkbox if applicable
      if (prevBackwardCol !== -1) {
        var backwardCheckboxCell = previousSheet.getRange(newRow, prevBackwardCol);
        backwardCheckboxCell.insertCheckboxes();
        backwardCheckboxCell.setValue(false);
      }
      
      // Only remove from current sheet after successfully adding to previous sheet
      currentSheet.deleteRow(row);
      
      // Release lock
      delete rowProcessingLock[lockKey];
      
      console.log('Moved item backward from ' + currentStageName + ' to ' + previousStageName);
      return true;
      
    } catch (error) {
      // Release lock on error
      delete rowProcessingLock[lockKey];
      console.log('Error moving row backward: ' + error.message);
      
      // Reset checkbox to prevent data loss
      try {
        currentSheet.getRange(row, backwardWorkflowCol).setValue(false);
      } catch (resetError) {
        console.log('Error resetting checkbox: ' + resetError.message);
      }
      return false;
    }
  }
  
  return false;
}

function getDataColumnCount(sheet) {
  var lastCol = sheet.getLastColumn();
  var dataColumns = 0;
  
  // Count columns that are not workflow columns
  for (var col = 1; col <= lastCol; col++) {
    var headerValue = sheet.getRange(1, col).getValue().toString();
    if (headerValue.indexOf('Move to') === -1 && 
        headerValue.indexOf('Complete') === -1 && 
        headerValue.indexOf('Move back to') === -1) {
      dataColumns++;
    } else {
      break; // Workflow columns are at the end
    }
  }
  
  return dataColumns;
}

function formatAllSheets() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  
  for (var i = 0; i < STAGE_NAMES.length; i++) {
    var sheet = ss.getSheetByName(STAGE_NAMES[i]);
    if (sheet) {
      formatSheet(sheet, i);
    }
  }
}

function formatSheet(sheet, stageIndex) {
  var lastRow = sheet.getLastRow();
  var lastCol = sheet.getLastColumn();
  
  if (lastRow > 0 && lastCol > 0) {
    // Format header row - removed green color (#4CAF50) and replaced with teal (#009688)
    var headerColors = ['#2196F3', '#FF9800', '#009688', '#9C27B0', '#F44336'];
    var headerColor = headerColors[stageIndex %% headerColors.length];
    
    sheet.getRange(1, 1, 1, lastCol).setBackground(headerColor)
         .setFontColor('white').setFontWeight('bold');
    
    // Set column widths
    for (var col = 1; col <= lastCol; col++) {
      var headerValue = sheet.getRange(1, col).getValue().toString();
      if (headerValue.indexOf('Move to') !== -1 || 
          headerValue.indexOf('Complete') !== -1 || 
          headerValue.indexOf('Move back to') !== -1) {
        sheet.setColumnWidth(col, 120); // Workflow columns
      } else {
        sheet.setColumnWidth(col, 150); // Data columns
      }
    }
  }
}

function refreshWorkflow() {
  // Clear any pending workflow actions and processing markers
  workflowQueue = [];
  processingRows = {};
  rowProcessingLock = {};
  isProcessingWorkflow = false;
  
  setupWorkflowSystem();
  SpreadsheetApp.getUi().alert('Workflow system refreshed!');
}

function clearAllData() {
  var ui = SpreadsheetApp.getUi();
  var result = ui.alert(
    'Clear All Data',
    'Are you sure you want to clear all data from all stages? This cannot be undone.',
    ui.ButtonSet.YES_NO
  );
  
  if (result === ui.Button.YES) {
    // Clear workflow queue, processing markers, and stop processing
    workflowQueue = [];
    processingRows = {};
    rowProcessingLock = {};
    isProcessingWorkflow = false;
    
    var ss = SpreadsheetApp.getActiveSpreadsheet();
    
    for (var i = 0; i < STAGE_NAMES.length; i++) {
      var sheet = ss.getSheetByName(STAGE_NAMES[i]);
      if (sheet) {
        var lastRow = sheet.getLastRow();
        if (lastRow > 1) {
          sheet.deleteRows(2, lastRow - 1);
        }
      }
    }
    
    ui.alert('All data cleared successfully!');
  }
}
`, stageNamesJS)

	return scriptContent
}
