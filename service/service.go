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
		fmt.Println(1)
		log.Fatalf("Unable to retrieve Sheets client: %v", err)
	}

	// Initialize Apps Script service
	scriptSrv, err := script.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		fmt.Println(2)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	// Create spreadsheet with 2 tabs
	spreadsheet := &sheets.Spreadsheet{
		Properties: &sheets.SpreadsheetProperties{
			Title: "Staged_Workflow_System",
		},
		Sheets: []*sheets.Sheet{
			{
				Properties: &sheets.SheetProperties{
					Title:   "Review Queue",
					SheetId: 0,
				},
			},
			{
				Properties: &sheets.SheetProperties{
					Title:   "Completed",
					SheetId: 1,
				},
			},
		},
	}

	// Create the spreadsheet
	resp, err := srv.Spreadsheets.Create(spreadsheet).Do()
	if err != nil {
		fmt.Println(3)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	fmt.Printf("✅ Spreadsheet created with 2 tabs!\n")
	fmt.Printf("📄 ID: %s\n", resp.SpreadsheetId)
	fmt.Printf("🔗 URL: https://docs.google.com/spreadsheets/d/%s\n", resp.SpreadsheetId)

	// Add sample data to Review Queue tab (2 columns + checkbox column)
	reviewQueueData := &sheets.ValueRange{
		Values: [][]interface{}{
			{"Item ID", "Description", "Complete"},
			{"REV001", "Contract Review - ABC Corp", false},
			{"REV002", "Budget Approval Request", false},
			{"REV003", "Policy Document Update", false},
			{"REV004", "Employee Performance Review", false},
			{"REV005", "Marketing Campaign Approval", false},
		},
	}

	_, err = srv.Spreadsheets.Values.Update(resp.SpreadsheetId, "Review Queue!A1", reviewQueueData).
		ValueInputOption("RAW").Do()
	if err != nil {
		fmt.Println(4)
		return http.StatusInternalServerError, "Internal server error", ""
	}

	// Add headers to Completed tab (3 columns + Back button column)
	completedData := &sheets.ValueRange{
		Values: [][]interface{}{
			{"Item ID", "Description", "Completion Date", "Action"},
		},
	}

	_, err = srv.Spreadsheets.Values.Update(resp.SpreadsheetId, "Completed!A1", completedData).
		ValueInputOption("RAW").Do()
	if err != nil {
		fmt.Println(5)
		log.Printf("Unable to write completed tasks data: %v", err)
	} else {
		fmt.Printf("✅ Completed tab headers added\n")
	}

	// Create Apps Script project and bind it to the spreadsheet
	scriptProject := &script.CreateProjectRequest{
		Title:    "Spreadsheet Automation Script",
		ParentId: resp.SpreadsheetId,
	}

	scriptResp, err := scriptSrv.Projects.Create(scriptProject).Do()
	if err != nil {
		if fmt.Sprintf("%v", err) == "googleapi: Error 403: Apps Script API has not been used in project 170457950855 before or it is disabled. Enable it by visiting https://console.developers.google.com/apis/api/script.googleapis.com/overview?project=170457950855 then retry. If you enabled this API recently, wait a few minutes for the action to propagate to our systems and retry." {
			fmt.Printf("⚠️  Apps Script API needs to be enabled\n")
			fmt.Printf("🔗 Please visit: https://console.developers.google.com/apis/api/script.googleapis.com/overview?project=170457950855\n")
			fmt.Printf("📋 Click 'ENABLE' button and wait 2-3 minutes, then run the program again\n")

			fmt.Println("6")
		} else {
			fmt.Println("7")
			log.Printf("Unable to create Apps Script project: %v", err)
		}
	} else {
		fmt.Printf("🔧 Apps Script project created: %s\n", scriptResp.ScriptId)

		// Add staged workflow Apps Script code with checkbox functionality
		scriptContent := `
function onOpen() {
  var ui = SpreadsheetApp.getUi();
  ui.createMenu('Workflow Tools')
    .addItem('Setup Workflow Checkboxes', 'setupWorkflowCheckboxes')
    .addItem('Refresh All Checkboxes', 'refreshAllCheckboxes')
    .addItem('Clear Completed Items', 'clearCompleted')
    .addToUi();
    
  // Auto-setup checkboxes when spreadsheet opens
  setupWorkflowCheckboxes();
}

function setupWorkflowCheckboxes() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  
  // Setup Review Queue checkboxes
  setupReviewQueueCheckboxes();
  
  // Setup Completed tab checkboxes
  setupCompletedCheckboxes();
  
  // Format the sheets
  formatWorkflowSheets();
  
  SpreadsheetApp.getUi().alert('Workflow checkboxes have been set up successfully!');
}

function setupReviewQueueCheckboxes() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var reviewSheet = ss.getSheetByName('Review Queue');
  var lastRow = reviewSheet.getLastRow();
  
  if (lastRow <= 1) return; // No data rows
  
  // Add checkboxes for each row
  for (var i = 2; i <= lastRow; i++) {
    var checkboxCell = reviewSheet.getRange(i, 3); // Column C
    checkboxCell.insertCheckboxes();
    checkboxCell.setValue(false); // Unchecked by default
  }
}

function setupCompletedCheckboxes() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var completedSheet = ss.getSheetByName('Completed');
  var lastRow = completedSheet.getLastRow();
  
  if (lastRow <= 1) return; // No data rows
  
  // Add checkboxes for each row
  for (var i = 2; i <= lastRow; i++) {
    var checkboxCell = completedSheet.getRange(i, 4); // Column D
    checkboxCell.insertCheckboxes();
    checkboxCell.setValue(false); // Unchecked by default
  }
}

function onEdit(e) {
  var range = e.range;
  var sheet = e.source.getActiveSheet();
  var row = range.getRow();
  var col = range.getColumn();
  var value = range.getValue();
  
  // Handle Review Queue checkbox clicks (move to completed when checked)
  if (sheet.getName() === 'Review Queue' && col === 3 && row > 1 && value === true) {
    moveToCompleted(row);
  }
  
  // Handle Completed checkbox clicks (move back to review when checked)
  if (sheet.getName() === 'Completed' && col === 4 && row > 1 && value === true) {
    moveBackToReview(row);
  }
}

function moveToCompleted(row) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var reviewSheet = ss.getSheetByName('Review Queue');
  var completedSheet = ss.getSheetByName('Completed');
  
  // Get the row data (2 columns: Item ID and Description)
  var rowData = reviewSheet.getRange(row, 1, 1, 2).getValues()[0];
  
  if (!rowData[0] || rowData[0] === '') {
    // Reset checkbox silently - no popup
    reviewSheet.getRange(row, 3).setValue(false);
    return;
  }
  
  // Add completion date as third column
  var completionDate = new Date().toLocaleDateString();
  var newRowData = [rowData[0], rowData[1], completionDate];
  
  // Add to Completed sheet
  var lastRow = completedSheet.getLastRow();
  var newRow = lastRow + 1;
  
  // Insert the data
  completedSheet.getRange(newRow, 1, 1, 3).setValues([newRowData]);
  
  // Add checkbox (unchecked)
  var checkboxCell = completedSheet.getRange(newRow, 4);
  checkboxCell.insertCheckboxes();
  checkboxCell.setValue(false);
  
  // Remove from Review Queue
  reviewSheet.deleteRow(row);
  
  // Refresh checkboxes in Review Queue
  setupReviewQueueCheckboxes();
  
  // No more popup alert - silent operation
}

function moveBackToReview(row) {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  var reviewSheet = ss.getSheetByName('Review Queue');
  var completedSheet = ss.getSheetByName('Completed');
  
  // Get the row data (first 2 columns only: Item ID and Description)
  var rowData = completedSheet.getRange(row, 1, 1, 2).getValues()[0];
  
  if (!rowData[0] || rowData[0] === '') {
    // Reset checkbox silently - no popup
    completedSheet.getRange(row, 4).setValue(false);
    return;
  }
  
  // Add back to Review Queue
  var lastRow = reviewSheet.getLastRow();
  var newRow = lastRow + 1;
  
  // Insert the data (only 2 columns)
  reviewSheet.getRange(newRow, 1, 1, 2).setValues([rowData]);
  
  // Add checkbox (unchecked)
  var checkboxCell = reviewSheet.getRange(newRow, 3);
  checkboxCell.insertCheckboxes();
  checkboxCell.setValue(false);
  
  // Remove from Completed
  completedSheet.deleteRow(row);
  
  // Refresh checkboxes in Completed sheet
  setupCompletedCheckboxes();
  
  // No more popup alert - silent operation
}

function formatWorkflowSheets() {
  var ss = SpreadsheetApp.getActiveSpreadsheet();
  
  // Format Review Queue
  var reviewSheet = ss.getSheetByName('Review Queue');
  reviewSheet.getRange('A1:C1').setBackground('#2196F3').setFontColor('white').setFontWeight('bold');
  reviewSheet.getRange('C1').setValue('Complete');
  reviewSheet.setColumnWidth(1, 100); // Item ID
  reviewSheet.setColumnWidth(2, 300); // Description
  reviewSheet.setColumnWidth(3, 100); // Checkbox column
  
  // Format Completed
  var completedSheet = ss.getSheetByName('Completed');
  completedSheet.getRange('A1:D1').setBackground('#4CAF50').setFontColor('white').setFontWeight('bold');
  completedSheet.getRange('D1').setValue('Return');
  completedSheet.setColumnWidth(1, 100); // Item ID
  completedSheet.setColumnWidth(2, 300); // Description
  completedSheet.setColumnWidth(3, 120); // Completion Date
  completedSheet.setColumnWidth(4, 100); // Checkbox column
}

function refreshAllCheckboxes() {
  setupReviewQueueCheckboxes();
  setupCompletedCheckboxes();
  SpreadsheetApp.getUi().alert('All checkboxes refreshed!');
}

function clearCompleted() {
  var ui = SpreadsheetApp.getUi();
  var result = ui.alert(
    'Clear Completed Items',
    'Are you sure you want to clear all completed items? This cannot be undone.',
    ui.ButtonSet.YES_NO
  );
  
  if (result === ui.Button.YES) {
    var ss = SpreadsheetApp.getActiveSpreadsheet();
    var completedSheet = ss.getSheetByName('Completed');
    var lastRow = completedSheet.getLastRow();
    
    if (lastRow > 1) {
      completedSheet.deleteRows(2, lastRow - 1);
      ui.alert('Completed items cleared successfully!');
    } else {
      ui.alert('No completed items to clear.');
    }
  }
}
`

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
			fmt.Printf("📝 Sample Apps Script code injected successfully!\n")
			fmt.Printf("🔗 Apps Script URL: https://script.google.com/d/%s/edit\n", scriptResp.ScriptId)
		}
	}

	fmt.Printf("\n🎉 Setup complete! Your staged workflow system includes:\n")
	fmt.Printf("   • Review Queue tab with 2 data columns + completion checkboxes\n")
	fmt.Printf("   • Completed tab with 3 data columns + return checkboxes\n")
	fmt.Printf("   • Automatic workflow movement when checkboxes are clicked\n")
	fmt.Printf("   • Professional formatting and intuitive checkbox interface\n")

	url := "https://docs.google.com/spreadsheets/d/" + resp.SpreadsheetId
	return http.StatusOK, "SpreadSheet Created", url
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
