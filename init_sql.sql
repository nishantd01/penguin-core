CREATE SCHEMA penguin;

CREATE TABLE penguin.snowflake_databases (
    database_name VARCHAR(255) PRIMARY KEY
);

CREATE TABLE penguin.role (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL
);

CREATE TABLE penguin.dev_logs (
    id INT AUTOINCREMENT PRIMARY KEY,
    timestamp TIMESTAMP,
    level VARCHAR(20),
    service_name VARCHAR(100),
    message VARCHAR(500)
);

INSERT INTO penguin.dev_logs (id,timestamp, level, service_name, message) VALUES
('39b17c2a-b542-4ec1-84ea-97d62b21db68','2025-08-15 10:15:00', 'INFO', 'auth-service', 'User login successful for user_id=12345'),
('f6ad93d1-ea1d-4e3e-b5b4-d00ae1f08a8d','2025-08-15 10:17:23', 'WARN', 'payment-service', 'Payment gateway timeout, retrying...'),
('4c76b5fa-b8f6-42f0-a221-67529cb3083b','2025-08-15 10:18:10', 'ERROR', 'order-service', 'Failed to create order_id=98765 due to DB constraint'),
('eebddc5a-c640-46d7-8d06-37d23cb9e69c','2025-08-15 10:19:42', 'INFO', 'notification-service', 'Email sent to user_id=12345'),
('b3d7eabd-011f-47f4-a25f-2ad3e172568a','2025-08-15 10:20:05', 'DEBUG', 'auth-service', 'Token generated for user_id=12345');

CREATE TABLE penguin.user (
    id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    email VARCHAR(255) NOT NULL UNIQUE,
    role_id UUID NOT NULL,
    FOREIGN KEY (role_id) REFERENCES penguin.role (id)
);

CREATE TABLE penguin.spreadsheet (
    id VARCHAR(255) PRIMARY KEY,
    report_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP,
    schema JSONB
);

CREATE TABLE penguin.spreadsheetpermissions (
    id UUID PRIMARY key,
    spreadsheet_id VARCHAR(255) NOT NULL,
    role_id UUID,
    columns_permissions TEXT,
    FOREIGN KEY (spreadsheet_id) REFERENCES penguin.spreadsheet (id),
    FOREIGN KEY (role_id) REFERENCES penguin.role (id)
)

-- Insert default roles
INSERT INTO penguin.role (id,name)
VALUES
('3f1c52b4-91c5-4de8-b1e2-813ea8b6e4a4','ADMIN 1'),
('b5d7cf7f-b2de-4a6c-8d44-0e8d3d1c7b12','ADMIN 2'),
('9c82cb57-0df3-4e86-8fa1-36ce983fc701','ADMIN 3'), 
('d1f3fbc5-4a1d-4e89-a2ef-9a4f6fdab123','ADMIN 4');

-- Insert default users
INSERT INTO penguin.user (id, name, email, role_id)
VALUES
('7e4f8a09-dc3f-4a17-bf90-21b7bc3cbd90', 'Kshitij Mathur', 'k.mathur68@gmail.com', '3f1c52b4-91c5-4de8-b1e2-813ea8b6e4a4'),
('54c3a7b0-6e59-4d29-95ef-5e85c8d469e6', 'Nishant Dehariya', 'nishantd02@gmail.com', 'b5d7cf7f-b2de-4a6c-8d44-0e8d3d1c7b12'),
('8b5fca38-4413-4f0c-a9e4-f67e2b674a45', 'Mayank Kumar', 'mayankmk165@gmail.com', '9c82cb57-0df3-4e86-8fa1-36ce983fc701'),
('2dc98baf-1d01-49b7-99cb-720e3e170df8', 'Sakshi', 'mehrasakshi5297@gmail.com', 'd1f3fbc5-4a1d-4e89-a2ef-9a4f6fdab123'),

-- Insert spreadsheets with proper schema format
-- INSERT INTO penguin.spreadsheet (id, report_name, created_at, schema)
-- VALUES
-- ('f2b85a4a-47b6-4c34-94e1-9a1b6e207c41', 'Sales Report', '2024-01-15 10:00:00', '{"date":"DATE","amount":"DECIMAL","product":"VARCHAR"}'),
-- ('a1d4902b-655a-4e9e-bf24-5f8e8b3cc33d', 'Marketing Data', '2024-01-16 11:00:00', '{"campaign":"VARCHAR","cost":"DECIMAL","roi":"DECIMAL"}');

-- Insert spreadsheet permissions with column names
-- INSERT INTO penguin.spreadsheetpermissions (id,spreadsheet_id, role_id, columns_permissions)
-- VALUES
-- ('c7b2ae35-6404-49b3-9c6b-86a967a0d8d4','a1d4902b-655a-4e9e-bf24-5f8e8b3cc33d', 'b5d7cf7f-b2de-4a6c-8d44-0e8d3d1c7b12', ARRAY['date','amount','product']),
-- ('6d0f8be9-7ac5-4e41-9e9f-51b0592cfb3c' ,'a1d4902b-655a-4e9e-bf24-5f8e8b3cc33d', 'd1f3fbc5-4a1d-4e89-a2ef-9a4f6fdab123', ARRAY['date','amount']);