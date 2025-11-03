# GCP IAP Setup Guide for mcp-grafana

This guide provides step-by-step instructions for configuring Google Cloud Platform's Identity-Aware Proxy (IAP) to work with mcp-grafana.

## Overview

When Grafana is protected by IAP, all requests must be authenticated with a valid IAP identity token. This guide covers:

1. Creating a service account for IAP access
2. Configuring IAM roles and permissions
3. Setting up OAuth client configuration
4. Configuring IAP access control policies
5. Testing the setup

## Prerequisites

- Access to Google Cloud Console with appropriate permissions
- `gcloud` CLI installed and configured
- Access to the GCP project containing the IAP-protected Grafana instance
- Permission to create service accounts and assign IAM roles

## Step 1: Create a Service Account

Create a service account that will be used to access the IAP-protected Grafana instance:

```bash
# Set your project ID
export PROJECT_ID="your-project-id"
export SERVICE_ACCOUNT_NAME="grafana-iap"
export SERVICE_ACCOUNT_EMAIL="${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# Create the service account
gcloud iam service-accounts create ${SERVICE_ACCOUNT_NAME} \
    --display-name="Grafana IAP Access" \
    --project=${PROJECT_ID}
```

## Step 2: Grant IAP Access Permission

Grant the service account permission to access IAP-protected resources:

```bash
# Grant the IAP HTTPS Resource Accessor role
gcloud projects add-iam-policy-binding ${PROJECT_ID} \
    --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
    --role="roles/iap.httpsResourceAccessor"
```

**Note:** Replace `${PROJECT_ID}` with your actual project ID. The project should be the one where IAP is configured for your Grafana instance.

## Step 3: Grant Service Account Token Creator Permission

Grant users (or another service account) permission to impersonate the service account and generate identity tokens:

```bash
# Grant token creator role to specific users
gcloud iam service-accounts add-iam-policy-binding ${SERVICE_ACCOUNT_EMAIL} \
    --member="user:username@example.com" \
    --role="roles/iam.serviceAccountTokenCreator"

# Or grant to a group
gcloud iam service-accounts add-iam-policy-binding ${SERVICE_ACCOUNT_EMAIL} \
    --member="group:developers@example.com" \
    --role="roles/iam.serviceAccountTokenCreator"
```

**Important:** Replace `username@example.com` or `developers@example.com` with the actual users or groups who need to use mcp-grafana.

## Step 4: Configure IAP Access Control Policies

Ensure the service account is allowed in IAP access control policies:

1. Navigate to [Google Cloud Console > IAP](https://console.cloud.google.com/security/iap)
2. Select your IAP-protected backend service or load balancer
3. Click "Edit Access" or "Add Principal"
4. Add the service account email: `${SERVICE_ACCOUNT_EMAIL}`
5. Grant the "IAP-secured Web App User" role
6. Save the changes

**Note:** If your IAP is configured with domain restrictions, the service account email must be explicitly added to the access control list, as service accounts don't match domain-based restrictions.

## Step 5: Get OAuth Client ID

You need the OAuth client ID that IAP uses. This is typically found in:

1. Google Cloud Console > APIs & Services > Credentials
2. Look for an OAuth 2.0 Client ID associated with IAP
3. Copy the Client ID (it looks like: `123456789012-abcdefghijklmnopqrstuvwxyz123456.apps.googleusercontent.com`)

Alternatively, you can find it by inspecting the IAP configuration:

```bash
# List IAP-protected backends
gcloud iap web get-iam-policy --resource-type=backend-services --project=${PROJECT_ID}
```

The OAuth client ID is used in the `--audiences` parameter when generating identity tokens.

## Step 6: Test Token Generation

Test that you can generate an IAP identity token:

```bash
# Generate an identity token
gcloud auth print-identity-token \
    --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} \
    --audiences="YOUR_OAUTH_CLIENT_ID" \
    --include-email

# Test accessing Grafana with the token
TOKEN=$(gcloud auth print-identity-token \
    --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} \
    --audiences="YOUR_OAUTH_CLIENT_ID" \
    --include-email)

curl -v -H "Authorization: Bearer ${TOKEN}" \
    https://your-grafana-instance.com/api/health
```

If successful, you should receive a response from Grafana instead of a 401/403 error.

## Step 7: Configure mcp-grafana

Once the GCP setup is complete, configure mcp-grafana to use IAP authentication.

### Option A: Using Command-Based Token Generation (Recommended)

This approach automatically refreshes tokens before expiration:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "https://your-grafana-instance.com",
```

### Option B: Using Static Token

For short-term testing, you can use a static token:

```bash
# Generate token once
export IAP_TOKEN=$(gcloud auth print-identity-token \
    --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} \
    --audiences="YOUR_OAUTH_CLIENT_ID" \
    --include-email)
```

Then use it in your configuration:

```json
{
  "mcpServers": {
    "grafana": {
      "command": "mcp-grafana",
      "args": [],
      "env": {
        "GRAFANA_URL": "https://your-grafana-instance.com",
        "GRAFANA_IAP_TOKEN": "${IAP_TOKEN}"
      }
    }
  }
}
```

**Note:** Static tokens expire after 1 hour. Use command-based generation for production.

## Troubleshooting

### 401/403 Errors

If you receive authentication errors:

1. **Verify Service Account Permissions:**
   ```bash
   # Check IAP access role
   gcloud projects get-iam-policy ${PROJECT_ID} \
       --flatten="bindings[].members" \
       --filter="bindings.members:serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
       --format="table(bindings.role)"
   
   # Check token creator role
   gcloud iam service-accounts get-iam-policy ${SERVICE_ACCOUNT_EMAIL}
   ```

2. **Verify IAP Access Control:**
   - Check that the service account email is listed in IAP access control policies
   - Ensure domain restrictions don't exclude service accounts (they must be explicitly added)

3. **Verify OAuth Client ID:**
   - Ensure the `--audiences` parameter matches the OAuth client ID configured for IAP
   - Check that the OAuth client allows programmatic access

4. **Check Token Generation:**
   ```bash
   # Verify token generation works
   gcloud auth print-identity-token \
       --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} \
       --audiences="YOUR_OAUTH_CLIENT_ID" \
       --include-email
   ```

### Token Expiration Issues

- IAP identity tokens expire after 1 hour
- When using `GRAFANA_IAP_TOKEN_COMMAND`, tokens are automatically cached and refreshed at 50 minutes
- If using static tokens, refresh them manually before expiration

### Command Execution Errors

If `GRAFANA_IAP_TOKEN_COMMAND` fails:

1. Ensure `gcloud` is installed and in PATH
2. Verify `gcloud` is authenticated: `gcloud auth list`
3. Check that the command executes successfully outside of mcp-grafana
4. Review mcp-grafana logs for specific error messages

### Organization Policies

Some organization policies may restrict service account usage:

- Check for policies like `iam.disableServiceAccountKeyCreation` (shouldn't affect impersonation)
- Verify service account creation is allowed in your project
- Ensure there are no policies blocking IAP access

## Security Best Practices

1. **Principle of Least Privilege:**
   - Only grant `roles/iam.serviceAccountTokenCreator` to users who need it
   - Limit service account permissions to what's necessary for Grafana access

2. **Access Control:**
   - Regularly review IAP access control policies
   - Remove access for users who no longer need it
   - Monitor audit logs for service account impersonation

3. **Token Management:**
   - Use command-based token generation instead of static tokens
   - Don't commit tokens to version control
   - Rotate service account keys if compromised

4. **Monitoring:**
   - Enable Cloud Audit Logs for IAP access
   - Monitor for unusual access patterns
   - Set up alerts for authentication failures

## Additional Resources

- [GCP IAP Documentation](https://cloud.google.com/iap/docs)
- [Service Account Impersonation](https://cloud.google.com/iam/docs/service-account-impersonation)
- [IAP Programmatic Access](https://cloud.google.com/iap/docs/authentication-howto)
- [mcp-grafana README](../README.md)

## Example: Complete Setup Script

Here's a complete script to set up IAP access:

```bash
#!/bin/bash
set -e

# Configuration
PROJECT_ID="your-project-id"
SERVICE_ACCOUNT_NAME="grafana-iap"
USER_EMAIL="your-email@example.com"
OAUTH_CLIENT_ID="your-oauth-client-id.apps.googleusercontent.com"
GRAFANA_URL="https://your-grafana-instance.com"

SERVICE_ACCOUNT_EMAIL="${SERVICE_ACCOUNT_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"

# Step 1: Create service account
echo "Creating service account..."
gcloud iam service-accounts create ${SERVICE_ACCOUNT_NAME} \
    --display-name="Grafana IAP Access" \
    --project=${PROJECT_ID} || echo "Service account may already exist"

# Step 2: Grant IAP access
echo "Granting IAP access..."
gcloud projects add-iam-policy-binding ${PROJECT_ID} \
    --member="serviceAccount:${SERVICE_ACCOUNT_EMAIL}" \
    --role="roles/iap.httpsResourceAccessor"

# Step 3: Grant token creator role
echo "Granting token creator role..."
gcloud iam service-accounts add-iam-policy-binding ${SERVICE_ACCOUNT_EMAIL} \
    --member="user:${USER_EMAIL}" \
    --role="roles/iam.serviceAccountTokenCreator"

# Step 4: Test token generation
echo "Testing token generation..."
TOKEN=$(gcloud auth print-identity-token \
    --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} \
    --audiences=${OAUTH_CLIENT_ID} \
    --include-email)

echo "Token generated successfully!"
echo ""
echo "Configure mcp-grafana with:"
echo "GRAFANA_URL=${GRAFANA_URL}"
echo "GRAFANA_IAP_TOKEN_COMMAND=\"gcloud auth print-identity-token --impersonate-service-account=${SERVICE_ACCOUNT_EMAIL} --audiences=${OAUTH_CLIENT_ID} --include-email\""
echo ""
echo "Testing Grafana access..."
curl -s -o /dev/null -w "%{http_code}" \
    -H "Authorization: Bearer ${TOKEN}" \
    ${GRAFANA_URL}/api/health

echo ""
echo "Setup complete!"
```

Save this script, update the variables at the top, make it executable (`chmod +x setup-iap.sh`), and run it to complete the setup.

