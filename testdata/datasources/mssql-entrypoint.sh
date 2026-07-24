#!/bin/bash

# Start SQL Server in the background
/opt/mssql/bin/sqlservr &

# Wait for it to be ready
echo "Waiting for SQL Server to start..."
# Continuously check for SQL Server availability
for i in {1..50};
do
    /opt/mssql-tools18/bin/sqlcmd -S localhost -U sa -P "$MSSQL_SA_PASSWORD" -C -Q "SELECT 1" > /dev/null 2>&1
    if [ $? -eq 0 ]
    then
        echo "SQL Server is ready (attempt $i)"
        break
    else
        echo "SQL Server is not ready yet (attempt $i)..."
        sleep 2
    fi
done

# Run the seed script
echo "Running seed script..."
/opt/mssql-tools18/bin/sqlcmd \
  -S localhost \
  -U sa \
  -P "$MSSQL_SA_PASSWORD" \
  -C \
  -i /init.sql

# Keep the container running by waiting for the background process
wait