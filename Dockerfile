# Use the official PostgreSQL image
FROM postgres:latest

# Set environment variables for the database
ENV POSTGRES_USER=postgres_user
ENV POSTGRES_PASSWORD=qwerty
ENV POSTGRES_DB=penguin

# Copy the SQL script into the container
COPY init_sql.sql /docker-entrypoint-initdb.d/

# Expose the default PostgreSQL port
EXPOSE 5432
