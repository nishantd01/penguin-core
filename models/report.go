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
