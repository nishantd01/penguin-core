CREATE SCHEMA penguin;

CREATE TABLE penguin.snowflake_databases (
    database_name VARCHAR(255) PRIMARY KEY
);

CREATE TABLE penguin.role (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL
);

CREATE TABLE penguin.user (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL UNIQUE,
    role_id UUID NOT NULL,
    FOREIGN KEY (role_id) REFERENCES penguin.role (id)
);

CREATE TABLE penguin.spreadsheet (
    id UUID PRIMARY KEY,
    report_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP,
    schema JSONB
);

CREATE TABLE penguin.spreadsheetpermissions (
    spreadsheet_id UUID,
    role_id UUID,
    columns_permissions TEXT,
    FOREIGN KEY (spreadsheet_id) REFERENCES penguin.spreadsheet (id),
    FOREIGN KEY (role_id) REFERENCES
)

-- Insert default roles
INSERT INTO penguin.roles (id name)
VALUES
(1, 'ADMIN 1'),
(2, 'ADMIN 1'),
(3, 'ADMIN 2'),
(4, 'ADMIN 3');

-- Insert default users
INSERT INTO penguin.users (id, name, email, rold_id)
VALUES
(1, 'Kshitij Mathur', 'k.mathur68@gmail.com', '1'),
(2, 'Nishant Dehariya', 'nishantd02@gmail.com', 2),
(3, 'Mayank Kumar', 'mayankmk165@gmail.com', 3),
(4, 'Pranav TV', 'to_be_filled@gmail.com', 4),

-- Insert spreadsheets with proper schema format
INSERT INTO penguin.spreadsheet (id, report_name, created_at, schema)
VALUES
('723e4567-e89b-12d3-a456-426614174000', 'Sales Report', '2024-01-15 10:00:00', '{"date":"DATE","amount":"DECIMAL","product":"VARCHAR"}'),
('823e4567-e89b-12d3-a456-426614174000', 'Marketing Data', '2024-01-16 11:00:00', '{"campaign":"VARCHAR","cost":"DECIMAL","roi":"DECIMAL"}');

-- Insert spreadsheet permissions with column names
INSERT INTO penguin.spreadsheetpermissions (spreadsheet_id, role_id, columns_permissions)
VALUES
('723e4567-e89b-12d3-a456-426614174000', '123e4567-e89b-12d3-a456-426614174000', ARRAY['date','amount','product']),
('723e4567-e89b-12d3-a456-426614174000', '223e4567-e89b-12d3-a456-426614174000', ARRAY['date','amount']),
('823e4567-e89b-12d3-a456-426614174000', '323e4567-e89b-12d3-a456-426614174000', ARRAY['campaign','cost']);
