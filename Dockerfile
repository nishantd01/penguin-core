# Use the official PostgreSQL image
FROM artifactory-dockerhub.cloud.capitalone.com/postgres:latest

# Set environment variables for the database
ENV POSTGRES_USER=
ENV POSTGRES_PASSWORD=
ENV POSTGRES_DB=

# Copy the SQL script into the container
COPY init_sql.sql /docker-entrypoint-initdb.d/

# Expose the default PostgreSQL port
EXPOSE 5432
