package models

type Column struct {
	Name       string   `json:"name"`
	DataType   string   `json:"type"`
	WritableBy []string `json:"writableBy"`
}

type Stage struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Roles       []string `json:"roles"`
}

type ReportInput struct {
	ReportName string   `json:"reportName"`
	SqlScript  string   `json:"sqlScript"` // could use this script or dummy table
	Columns    []Column `json:"columns"`
	Stages     []Stage  `json:"stages"`
}

type ViewPermissionRequest struct {
	SpreadsheetID string `json:"spreadsheet_id"`
	TabName       string `json:"tab_name"` // This will be the stage/tab name
	EmailID       string `json:"email_id"`
}

type ViewPermissionResponse struct {
	CanView       bool   `json:"can_view"`
	SpreadsheetID string `json:"spreadsheet_id"`
	TabName       string `json:"tab_name"`
	EmailID       string `json:"email_id"`
	Message       string `json:"message"`
}
