package utils

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"google.golang.org/api/script/v1"
	"google.golang.org/api/sheets/v4"
)

// var client *http.Client

func WriteDataToSheet(
	spreadsheetID string,
	sheetName string,
	startCell string,
	data [][]interface{},
) error {
	ctx := context.Background()
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Printf("Unable to read client secret file: %v", err)
		return errors.New(err.Error())
	}

	// IMPORTANT: Add script.ScriptProjectsScope to have permission to create/update Apps Script projects
	config, err := google.ConfigFromJSON(b,
		drive.DriveFileScope,
		script.ScriptProjectsScope,
	)
	if err != nil {
		log.Printf("Unable to parse client secret file to config: %v", err)
		return errors.New(err.Error())
	}

	client := getClient(config)
	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("unable to create Sheets service: %w", err)
	}

	writeRange := fmt.Sprintf("'%s'!%s", sheetName, startCell)

	valueRange := &sheets.ValueRange{
		Range:  writeRange,
		Values: data,
	}

	_, err = sheetsService.Spreadsheets.Values.Update(spreadsheetID, writeRange, valueRange).
		ValueInputOption("RAW").
		Do()
	if err != nil {
		return fmt.Errorf("failed to write data to sheet: %w", err)
	}

	// Step 2: Get sheet ID (needed for formatting)
	spreadsheet, err := sheetsService.Spreadsheets.Get(spreadsheetID).Do()
	if err != nil {
		return fmt.Errorf("failed to retrieve spreadsheet: %w", err)
	}

	var sheetID int64 = -1
	for _, s := range spreadsheet.Sheets {
		if s.Properties.Title == sheetName {
			sheetID = s.Properties.SheetId
			break
		}
	}
	if sheetID == -1 {
		return fmt.Errorf("sheet %s not found", sheetName)
	}

	// Step 3: Calculate row/column range
	startCol, startRow := parseCell(startCell)
	numRows := int64(len(data))
	numCols := int64(0)
	if numRows > 0 {
		numCols = int64(len(data[0]))
	}

	// Step 4: Apply text wrapping
	wrapRequest := &sheets.Request{
		RepeatCell: &sheets.RepeatCellRequest{
			Range: &sheets.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    startRow - 1,
				EndRowIndex:      startRow - 1 + numRows,
				StartColumnIndex: startCol - 1,
				EndColumnIndex:   startCol - 1 + numCols,
			},
			Cell: &sheets.CellData{
				UserEnteredFormat: &sheets.CellFormat{
					WrapStrategy: "WRAP",
				},
			},
			Fields: "userEnteredFormat.wrapStrategy",
		},
	}

	_, err = sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{wrapRequest},
	}).Do()
	if err != nil {
		return fmt.Errorf("failed to apply text wrapping: %w", err)
	}

	log.Printf("✅ Data written to spreadsheet %s at range %s", spreadsheetID, writeRange)
	return nil
}

func parseCell(cell string) (col int64, row int64) {
	letters := ""
	numbers := ""

	for _, r := range cell {
		if r >= 'A' && r <= 'Z' {
			letters += string(r)
		} else if r >= '0' && r <= '9' {
			numbers += string(r)
		}
	}

	col = int64(0)
	for i := 0; i < len(letters); i++ {
		col *= 26
		col += int64(letters[i]-'A') + 1
	}

	fmt.Sscanf(numbers, "%d", &row)
	return
}

func UploadSheet(reportName string, title string) (string, error) {
	ctx := context.Background()

	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Printf("Unable to read client secret file: %v", err)
		return "", errors.New(err.Error())
	}

	// IMPORTANT: Add script.ScriptProjectsScope to have permission to create/update Apps Script projects
	config, err := google.ConfigFromJSON(b,
		drive.DriveFileScope,
		script.ScriptProjectsScope,
	)
	if err != nil {
		log.Printf("Unable to parse client secret file to config: %v", err)
		return "", errors.New(err.Error())
	}

	client := getClient(config)

	driveService, err := drive.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Unable to create Drive service: %v", err)
		return "", errors.New(err.Error())
	}

	folderID := "1hGITz-qza0wMpK9MW93za5iq9u9-3qcg"
	// Create a new Google Sheet file inside your folder
	fileMetadata := &drive.File{
		Name:     reportName,
		MimeType: "application/vnd.google-apps.spreadsheet",
		Parents:  []string{folderID},
	}

	file, err := driveService.Files.Create(fileMetadata).Do()
	if err != nil {
		log.Printf("Unable to create spreadsheet: %v", err)
		return "", errors.New(err.Error())
	}

	fmt.Printf("Spreadsheet created!\nID: %s\nURL: https://docs.google.com/spreadsheets/d/%s\n",
		file.Id, file.Id)

	// Create Apps Script service
	scriptService, err := script.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		log.Printf("Unable to create Apps Script service: %v", err)
		return "", errors.New(err.Error())
	}

	var project *script.Project

	createReq := &script.CreateProjectRequest{
		Title:    title,
		ParentId: file.Id, // This binds the script to the spreadsheet
	}

	project, err = scriptService.Projects.Create(createReq).Do()
	if err != nil {
		// If the error is because the project already exists, we'll get it by ID
		// Note: This is a simplified approach - in production you'd want better error handling
		fmt.Println("Project likely already exists, attempting to find it...")

		// Get the script ID from the spreadsheet's manifest
		// This is a more reliable way to find the bound script
		manifest, err := scriptService.Projects.GetContent(file.Id).Do()
		if err != nil {
			log.Printf("Failed to get script manifest: %v", err)
			return "", errors.New(err.Error())
		}

		if manifest != nil && manifest.ScriptId != "" {
			project, err = scriptService.Projects.Get(manifest.ScriptId).Do()
			if err != nil {
				log.Printf("Failed to get existing project: %v", err)
				return "", errors.New(err.Error())
			}
			fmt.Printf("Using existing Apps Script project: ScriptID=%s\n", project.ScriptId)
		} else {
			log.Printf("Could not determine existing script ID")
			return "", errors.New(err.Error())
		}
	} else {
		fmt.Printf("Created new Apps Script project: ScriptID=%s\n", project.ScriptId)
	}

	var appscriptString = createAppScript()

	scriptFiles := []*script.File{
		{
			Name:   "Code",
			Type:   "SERVER_JS",
			Source: appscriptString,
		},
		{
			Name:   "appsscript",
			Type:   "JSON",
			Source: `{"timeZone":"America/New_York","exceptionLogging":"CLOUD"}`,
		},
	}

	content := &script.Content{
		Files: scriptFiles,
	}

	_, err = scriptService.Projects.UpdateContent(project.ScriptId, content).Do()
	if err != nil {
		log.Printf("Failed to update script content: %v", err)
		return "", errors.New(err.Error())
	}

	fmt.Println("Injected Apps Script code successfully!")
	fmt.Printf("Open your sheet and check Extensions > Apps Script to see the code.\n")

	return file.Id, nil
}

func ProtectHeaderRow(spreadsheetID, sheetName string, data [][]interface{}) error {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Printf("Unable to read client secret file: %v", err)
		return errors.New(err.Error())
	}

	// IMPORTANT: Add script.ScriptProjectsScope to have permission to create/update Apps Script projects
	config, err := google.ConfigFromJSON(b,
		drive.DriveFileScope,
		script.ScriptProjectsScope,
	)
	if err != nil {
		log.Printf("Unable to parse client secret file to config: %v", err)
		return errors.New(err.Error())
	}

	client := getClient(config)
	ctx := context.Background()
	sheetsService, err := sheets.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("failed to create Sheets service: %w", err)
	}

	// Get the sheet ID from the sheet name
	spreadsheet, err := sheetsService.Spreadsheets.Get(spreadsheetID).Do()
	if err != nil {
		return fmt.Errorf("failed to get spreadsheet details: %w", err)
	}

	var sheetID int64 = -1
	for _, sheet := range spreadsheet.Sheets {
		if sheet.Properties.Title == sheetName {
			sheetID = sheet.Properties.SheetId
			break
		}
	}
	if sheetID == -1 {
		return fmt.Errorf("sheet '%s' not found", sheetName)
	}

	// Define the protected range (row 1)
	requests := []*sheets.Request{
		{
			AddProtectedRange: &sheets.AddProtectedRangeRequest{
				ProtectedRange: &sheets.ProtectedRange{
					Range: &sheets.GridRange{
						SheetId:          sheetID,
						StartRowIndex:    0,
						EndRowIndex:      1, // only row 1
						StartColumnIndex: 0,
						// EndColumnIndex: optional, can leave open to protect all columns
					},
					Description:           "Protect header row",
					WarningOnly:           false,             // true = users can edit with warning
					Editors:               &sheets.Editors{}, // no editors → owner only
					RequestingUserCanEdit: true,
				},
			},
		},
	}

	batchRequest := &sheets.BatchUpdateSpreadsheetRequest{Requests: requests}
	_, err = sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, batchRequest).Do()
	if err != nil {
		return fmt.Errorf("failed to apply protected range: %w", err)
	}

	numCols := len(data[0])

	boldReq := &sheets.Request{
		RepeatCell: &sheets.RepeatCellRequest{
			Range: &sheets.GridRange{
				SheetId:          sheetID,
				StartRowIndex:    0,
				EndRowIndex:      1,
				StartColumnIndex: 0,
				EndColumnIndex:   int64(numCols),
			},
			Cell: &sheets.CellData{
				UserEnteredFormat: &sheets.CellFormat{
					TextFormat: &sheets.TextFormat{
						Bold: true,
					},
				},
			},
			Fields: "userEnteredFormat.textFormat.bold",
		},
	}

	req := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{boldReq},
	}

	_, err = sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, req).Do()
	if err != nil {
		return fmt.Errorf("failed to bold header row: %w", err)
	}

	log.Printf("✅ First row protected successfully in sheet '%s'", sheetName)
	return nil
}

func AddSheetToSpreadsheet(spreadsheetID, sheetName string) error {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Printf("Unable to read client secret file: %v", err)
		return errors.New(err.Error())
	}

	// IMPORTANT: Add script.ScriptProjectsScope to have permission to create/update Apps Script projects
	config, err := google.ConfigFromJSON(b,
		drive.DriveFileScope,
		script.ScriptProjectsScope,
	)
	if err != nil {
		log.Printf("Unable to parse client secret file to config: %v", err)
		return errors.New(err.Error())
	}

	client := getClient(config)
	sheetsService, err := sheets.NewService(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return err
	}

	addSheetReq := &sheets.BatchUpdateSpreadsheetRequest{
		Requests: []*sheets.Request{
			{
				AddSheet: &sheets.AddSheetRequest{
					Properties: &sheets.SheetProperties{
						Title: sheetName,
					},
				},
			},
		},
	}

	_, err = sheetsService.Spreadsheets.BatchUpdate(spreadsheetID, addSheetReq).Do()
	return err
}

func createAppScript() string {
	scp := `/**
	* Creates an installable onEdit trigger for the 'restrictColumnEditingToUser' function.
	* This is designed to be called by an onOpen trigger to set up the environment.
	*/
   function createOnEditTrigger() {
	 // First, delete any existing triggers to prevent duplicates.
	 const triggers = ScriptApp.getProjectTriggers();
   
	 // Use forEach to iterate through the triggers, which is a very compatible method.
	 triggers.forEach(function(trigger) {
	   if (trigger.getHandlerFunction() === 'restrictColumnEditingToUser') {
		 ScriptApp.deleteTrigger(trigger);
	   }
	 });
   
	 // Then, create a new installable onEdit trigger.
	 ScriptApp.newTrigger('restrictColumnEditingToUser')
		 .forSpreadsheet(SpreadsheetApp.getActive())
		 .onEdit()
		 .create();
   }

   function checkAccess(emailId,sheetId, columnName) {
	const url = "https://7028edfe3d71.ngrok-free.app/api/v1/check-edit-permission"; // Update this to your actual endpoint
	const payload = {
	  email: emailId,
	  sheet_id: sheetId,
	  column_name: columnName
	};
  
	const options = {
	  method: "post",
	  contentType: "application/json",
	  payload: JSON.stringify(payload),
	  muteHttpExceptions: true
	};
  
  
	var status = 201;
	try {
	  const response = UrlFetchApp.fetch(url, options);
	  status = response.getResponseCode();
	  const body = response.getContentText();
  
  
	} catch (error) {
	  Logger.log("Error: " + error.message);
	}
	return status
  }
   
   /**
	* An onOpen trigger that runs when the spreadsheet is opened.
	* It will call the function to create the installable trigger.
	*/
   function onOpen() {
	 

	 SpreadsheetApp.getUi()
	 .createMenu("Column Permission setup")
	 .addItem('Enable Edit Trigger', 'createOnEditTrigger')
	 .addToUi();

	//  createOnEditTrigger();

	//  SpreadsheetApp.getUi()
	//  .createMenu("Test Menu")
	//  .addItem("Say Hello", "sayHello")
	//  .addToUi();
   }
   
   // Your original function to restrict column editing.
   function restrictColumnEditingToUser(e) {

	if (typeof e === 'undefined') {
		return;
	}



	// var response = UrlFetchApp.fetch("https://0ac96c85d198.ngrok-free.app");
  
	// get SheetId
	const spreadsheetId = e.source;
	const sheetId = spreadsheetId.getId();
	// var spreadsheetId = SpreadsheetApp.getActiveSpreadsheet().getId()
	// var spreadsheetId = SpreadsheetApp.getActiveSpreadsheet().getId()
  
	//getemailId
	var emailid = Session.getActiveUser().getEmail();
	// var emailid = e.User;
	console.log(e.user)
  
	// get First Row of the edited column
	var editedRange = e.range;
	var editedSheet = editedRange.getSheet();
	const columnEdited = e.range.getColumn();
	var firstRowCell = editedSheet.getRange(1, columnEdited);
	var editedColumnName = firstRowCell.getValue();
  
	// log
	console.log("spreadsheetId",sheetId, "emailId", emailid, "colEdited", editedColumnName)
  
	// if non 200 otherwise toast the message and avoid edit / revert edit done 
	// var response = UrlFetchApp.fetch("https://google.com");
	// Logger.log("Response code: " + response.getResponseCode());
  
	code = checkAccess(emailid,sheetId,editedColumnName);
  
	if (code !== 200) {
	  console.log(" should not be allowed to edit")
	  editedRange.setValue(e.oldValue || ""); // May require enabling 'Undo' logic or storing value elsewhere
	  SpreadsheetApp.getActiveSpreadsheet().toast("Edit to column " + editedColumnName + " is not permitted.");
	} else {
	  console.log(" User has Access ")
	}
  
  
  }`

	return scp
}

func getClient(config *oauth2.Config) *http.Client {
	usr, _ := user.Current()
	tokenCacheDir := filepath.Join(usr.HomeDir, ".credentials")
	os.MkdirAll(tokenCacheDir, os.ModePerm)
	tokenPath := filepath.Join(tokenCacheDir, "token.json")
	fmt.Printf("token %v\n", tokenPath)

	var token *oauth2.Token
	f, err := os.Open(tokenPath)
	if err == nil {
		defer f.Close()
		json.NewDecoder(f).Decode(&token)
	} else {
		authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
		fmt.Printf("Go to the following URL in your browser then enter the authorization code:\n%v\n", authURL)

		var code string
		fmt.Print("Enter code: ")
		fmt.Scan(&code)

		tok, err := config.Exchange(context.TODO(), code)
		if err != nil {
			log.Printf("Unable to retrieve token from web: %v", err)
		}
		token = tok

		f, err := os.Create(tokenPath)
		if err == nil {
			defer f.Close()
			json.NewEncoder(f).Encode(token)
		}
	}
	return config.Client(context.Background(), token)
}
