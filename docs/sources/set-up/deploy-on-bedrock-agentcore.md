---
title: Deploy on Amazon Bedrock AgentCore Runtime
menuTitle: Bedrock AgentCore
description: Run the Grafana MCP server as an MCP tool server hosted on Amazon Bedrock AgentCore Runtime.
keywords:
  - AWS
  - Bedrock
  - AgentCore
  - MCP
  - SigV4
weight: 6
aliases: []
---

# Deploy on Amazon Bedrock AgentCore Runtime

Run the Grafana MCP server as a hosted MCP tool server on [Amazon Bedrock AgentCore Runtime](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-mcp.html), so agents built with Bedrock (or any client that can invoke an AgentCore runtime) can call Grafana tools.

## What you'll achieve

You package the Grafana MCP server as a container that meets AgentCore's MCP contract, push it to Amazon ECR, and create an AgentCore runtime that serves the tools over streamable HTTP.

## Before you begin

- **AgentCore in a supported Region.** AgentCore is generally available (no preview opt-in). Confirm your Region is supported in [Supported AWS Regions](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/agentcore-regions.html); use the same Region for ECR and the runtime.
- **AWS CLI and Docker** (with `buildx`) installed, and AWS credentials configured.
- **An IAM execution role** for the runtime, with a trust policy that allows the `bedrock-agentcore.amazonaws.com` service principal to assume it, plus permissions to pull from ECR and write CloudWatch Logs. The runtime can't be created without it. See the [Bedrock AgentCore developer guide](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/) for IAM policy details.
- **A Grafana instance and a service account token.**
- **Security: AgentCore terminates inbound auth.** The Grafana MCP server enforces no authentication of its own. When you omit an authorizer configuration, the runtime uses AWS IAM (SigV4) auth by default, so only callers with permission to invoke the runtime can reach the server. Never expose the container port directly: the service account token baked into the runtime grants full Grafana access to anyone who can reach it.

## How AgentCore runs the server

AgentCore expects an MCP container that serves streamable HTTP on `0.0.0.0:8000/mcp`, built for `linux/arm64`. It routes requests through the [InvokeAgentRuntime](https://docs.aws.amazon.com/bedrock-agentcore/latest/APIReference/API_InvokeAgentRuntime.html) API and injects an `Mcp-Session-Id` header for session affinity; in stateless mode the server accepts that header without rejecting it.

Two things mean you can't run the stock `grafana/mcp-grafana` image directly:

- The stock image starts in SSE mode, and the server's transport and address are set by command-line flags, not environment variables.
- `CreateAgentRuntime` takes a container image but has no field to override the container's command.

So you build a small wrapper image that bakes the correct start command. Everything else is a normal ECR push and runtime creation.

## Build and push the container image

Set shared variables:

```bash
export AWS_ACCOUNT_ID=<your account id>
export AWS_REGION=<your region>            # e.g. us-east-1
export ECR_REPO=grafana-mcp-agentcore
export ECR_URI=$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO
```

Create a `Dockerfile` that wraps the official image and sets the AgentCore-correct command:

```dockerfile
FROM grafana/mcp-grafana:latest

# AgentCore needs streamable HTTP on 0.0.0.0:8000/mcp and can't pass args, so bake them here.
# --disable-proxied = stateless mode (AgentCore's default); drops proxied tools like Tempo.
ENTRYPOINT ["/app/mcp-grafana", \
  "--transport", "streamable-http", \
  "--address", "0.0.0.0:8000", \
  "--endpoint-path", "/mcp", \
  "--disable-proxied"]
```

{{< admonition type="note" >}}
This Dockerfile targets the current release (v0.17.0), which has no Host allowlist. Versions with DNS-rebinding protection (added after v0.17.0) reject AgentCore's non-loopback `Host` with `403`, which also gates `/healthz`. Check your image with `docker run --rm --entrypoint /app/mcp-grafana grafana/mcp-grafana:<tag> --help | grep allowed-hosts` (the `--entrypoint` override keeps `--help` from being appended to the image's baked start command). If it lists `--allowed-hosts`, append `"--allowed-hosts", "*"` to the `ENTRYPOINT` — safe only because AgentCore fronts and authenticates the container; never use `"*"` on a directly-exposed port. Appending it to an image that lacks the flag makes the container exit at startup with `flag provided but not defined: -allowed-hosts` (visible in the runtime's CloudWatch logs), so run the check first.
{{< /admonition >}}

Authenticate to ECR, create the repository, then build for `arm64` and push:

```bash
aws ecr get-login-password --region $AWS_REGION \
  | docker login --username AWS --password-stdin $AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com

aws ecr create-repository --repository-name $ECR_REPO --region $AWS_REGION

docker buildx build --platform linux/arm64 -t $ECR_URI:latest --push .
```

For production, pin `grafana/mcp-grafana:latest` and your image tag to explicit versions.

## Create the runtime

Set the execution role and Grafana connection, then create the runtime:

```bash
export EXECUTION_ROLE_ARN=arn:aws:iam::$AWS_ACCOUNT_ID:role/<your-agentcore-execution-role>
export GRAFANA_URL=https://<your-instance>.grafana.net
export GRAFANA_SERVICE_ACCOUNT_TOKEN=<your service account token>

aws bedrock-agentcore-control create-agent-runtime \
  --agent-runtime-name mcp_grafana \
  --agent-runtime-artifact '{"containerConfiguration":{"containerUri":"'"$ECR_URI"':latest"}}' \
  --role-arn "$EXECUTION_ROLE_ARN" \
  --network-configuration '{"networkMode":"PUBLIC"}' \
  --protocol-configuration '{"serverProtocol":"MCP"}' \
  --environment-variables \
      GRAFANA_URL="$GRAFANA_URL",GRAFANA_SERVICE_ACCOUNT_TOKEN="$GRAFANA_SERVICE_ACCOUNT_TOKEN" \
  --region $AWS_REGION
```

Notes:

- `--agent-runtime-name` must match `[a-zA-Z][a-zA-Z0-9_]{0,47}` (letters, digits, and underscores; no hyphens).
- The response returns an `agentRuntimeArn` and a `status` that moves from `CREATING` to `READY`.
- There is no port, path, or command field: the `0.0.0.0:8000/mcp` contract is met by the wrapper image.

## Authentication

### Single-tenant (recommended)

The `create-agent-runtime` command above bakes `GRAFANA_URL` and `GRAFANA_SERVICE_ACCOUNT_TOKEN` into the runtime environment. Every request uses that one Grafana instance and token. Inbound access to the runtime is controlled by AWS IAM (SigV4), because no authorizer configuration was supplied. To require OAuth/JWT bearer tokens instead, add an `--authorizer-configuration` to the create command; see [Inbound and outbound auth](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-mcp.html#runtime-mcp-auth-error-responses).

### Multi-tenant access

Most deployments use the single-tenant model above. Use per-request headers only if a fronting gateway injects per-caller Grafana identity. The server reads `X-Grafana-URL` and `X-Grafana-Service-Account-Token` from each request (`ExtractGrafanaInfoFromHeaders`), falling back to the environment variables when a header is absent. Per-request identity pairs with stateful mode. For header details, refer to [Multi-organization and headers](../../configure/multi-organization-and-headers/).

## Verify

After the runtime reports `READY`, invoke it and confirm tools are listed. Because a deployed runtime is reached through the SigV4-signed `InvokeAgentRuntime` endpoint, follow [Invoke your deployed MCP server](https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/runtime-mcp.html#runtime-mcp-invoke-server) or connect with the MCP Inspector.

To validate the container contract locally before deploying (this doesn't exercise AgentCore's ingress), run the image detached and check `/healthz`:

```bash
docker run -d --rm --name mcp-grafana-test --platform linux/arm64 -p 8000:8000 \
  -e GRAFANA_URL=$GRAFANA_URL \
  -e GRAFANA_SERVICE_ACCOUNT_TOKEN=$GRAFANA_SERVICE_ACCOUNT_TOKEN \
  $ECR_URI:latest

curl -sS http://0.0.0.0:8000/healthz   # expect: ok

docker stop mcp-grafana-test
```

The current release has no Host allowlist, so any request reaches `/healthz`. On images with the allowlist (see the note above), `--allowed-hosts "*"` behaves the same; if you pinned it to specific values, add `-H "Host: <pinned-value>"` to the `curl`.

## Modes and behavior

AgentCore recommends stateless MCP servers, which is why the wrapper image sets `--disable-proxied`:

| Mode | How to run | Trade-off |
| --- | --- | --- |
| Stateless | `--disable-proxied` (in the wrapper image) | AgentCore's recommended default. Proxied tools (for example, Tempo) aren't available. |
| Stateful | Leave proxied tools enabled | Proxied tools are available. Requires the client to reuse the `Mcp-Session-Id` AgentCore returns. |

On versions with DNS-rebinding protection, `--allowed-origins` is empty by default, which rejects any request that carries an `Origin` header. Non-browser MCP clients don't send one, so this doesn't affect AgentCore; set `--allowed-origins` if a browser-based or Origin-adding client connects.

## Next steps

- [Configure transports and addresses](../../configure/transports-and-addresses/) for the streamable HTTP options used here.
- [Health check endpoint](../../configure/health-check-endpoint/) for readiness probes.
- [Configure authentication](../../configure/authentication/) for service accounts and tokens.
- [Multi-organization and headers](../../configure/multi-organization-and-headers/) for per-request Grafana identity.
