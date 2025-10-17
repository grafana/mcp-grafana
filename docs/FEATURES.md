# Features & Capabilities

This guide provides detailed information about all features available in the Grafana MCP server.

> **Note:** This list is for informational purposes only and does not represent a roadmap or commitment to future features.

## Table of Contents

- [Dashboards](#dashboards)
- [Datasources](#datasources)
- [Prometheus Querying](#prometheus-querying)
- [Loki Querying](#loki-querying)
- [Pyroscope Profiling](#pyroscope-profiling)
- [Incidents](#incidents)
- [Sift Investigations](#sift-investigations)
- [Alerting](#alerting)
- [Grafana OnCall](#grafana-oncall)
- [Admin](#admin)
- [Navigation](#navigation)
- [Asserts](#asserts)

## Dashboards

Comprehensive dashboard management capabilities for searching, retrieving, and modifying Grafana dashboards.

### Available Operations

#### Search for Dashboards
Find dashboards by title, tags, or other metadata. Useful for discovering existing dashboards before creating new ones or understanding your dashboard landscape.

**Use cases:**
- Find all dashboards related to a specific service
- Discover dashboards by tag
- List recently modified dashboards

#### Get Dashboard by UID
Retrieve the complete dashboard JSON including all panels, variables, and settings.

> **⚠️ Warning:** Large dashboards can consume significant context window space. Consider using `get_dashboard_summary` or `get_dashboard_property` instead when possible.

**Use cases:**
- Full dashboard inspection
- Extracting complete configuration for backup
- Deep analysis of dashboard structure

#### Get Dashboard Summary
Get a compact overview of a dashboard without the full JSON, including:
- Dashboard title and description
- Panel count and types
- Variables used
- Metadata (tags, created/modified dates, version)

**Use cases:**
- Quick dashboard overview
- Planning dashboard modifications
- Understanding dashboard structure without loading full JSON

#### Get Dashboard Property
Extract specific parts of a dashboard using JSONPath expressions. This allows surgical data extraction without loading the entire dashboard.

**JSONPath examples:**
- `$.title` - Get dashboard title
- `$.panels[*].title` - Get all panel titles
- `$.templating.list[*].name` - Get all variable names
- `$.panels[?(@.type=='graph')].title` - Get titles of all graph panels

**Use cases:**
- Extract specific panel configurations
- Get variable definitions
- Retrieve dashboard metadata selectively

#### Update or Create Dashboard
Modify existing dashboards or create new ones with full dashboard JSON.

> **⚠️ Warning:** Requires complete dashboard JSON which can consume large amounts of context window space. Consider using `patch_dashboard` for targeted modifications.

**Use cases:**
- Creating new dashboards programmatically
- Bulk dashboard updates
- Dashboard migration

#### Patch Dashboard
Apply specific changes to a dashboard without requiring the full JSON. This significantly reduces context window usage for targeted modifications.

**Use cases:**
- Update dashboard title or description
- Add a new panel
- Modify specific variables
- Update time range settings

#### Get Panel Queries and Datasource Info
Extract query information from all panels in a dashboard, including:
- Panel title
- Query expressions
- Datasource UID and type
- Query language (PromQL, LogQL, etc.)

**Use cases:**
- Understanding what data a dashboard queries
- Auditing datasource usage
- Query optimization analysis

### Context Window Management

The dashboard tools implement several strategies to manage AI context window usage effectively:

#### Best Practices

1. **Start with `get_dashboard_summary`**
   - Get an overview before detailed operations
   - Plan modifications based on summary
   - Understand dashboard structure without full JSON

2. **Use `get_dashboard_property` with JSONPath**
   - Extract only the specific data you need
   - Avoid loading unnecessary dashboard parts
   - Reduce token consumption significantly

3. **Avoid `get_dashboard_by_uid` unless necessary**
   - Only use when you specifically need complete dashboard JSON
   - Consider if `get_dashboard_property` can meet your needs
   - Large dashboards can use thousands of tokens

4. **Use `patch_dashboard` for modifications**
   - Make targeted changes without full JSON
   - Significantly more efficient than full updates
   - Reduces error potential

## Datasources

Manage and query Grafana datasources with support for multiple datasource types.

### Supported Datasource Types

- **Prometheus** - Metrics and time series data
- **Loki** - Log aggregation and querying
- **Pyroscope** - Continuous profiling data

### Available Operations

#### List Datasources
View all configured datasources in your Grafana instance, including:
- Datasource name and UID
- Datasource type
- URL and access mode
- Basic settings

**Use cases:**
- Discover available datasources
- Audit datasource configuration
- Find datasource UIDs for queries

#### Get Datasource by UID
Retrieve detailed information about a specific datasource using its unique identifier.

**Use cases:**
- Verify datasource configuration
- Check datasource connectivity settings
- Extract authentication details

#### Get Datasource by Name
Retrieve datasource information using its name instead of UID.

**Use cases:**
- Look up datasource when you know the name
- Convert names to UIDs for other operations

## Prometheus Querying

Execute PromQL queries and retrieve metric metadata from Prometheus datasources.

### Available Operations

#### Query Prometheus
Execute PromQL queries with support for:
- **Instant queries** - Get metric values at a specific time
- **Range queries** - Get metric values over a time range

**Query types:**
- Counter queries
- Gauge queries
- Histogram queries
- Summary queries
- Aggregations
- Functions and operators

**Use cases:**
- Get current CPU usage
- Retrieve memory usage over time
- Calculate request rate
- Monitor error rates
- Custom metric analysis

#### List Prometheus Metric Metadata
Retrieve metadata about metrics including:
- Metric type (counter, gauge, histogram, summary)
- Help text
- Unit information

**Use cases:**
- Understand metric types
- Discover metric documentation
- Validate metric usage

#### List Prometheus Metric Names
Get a list of all available metric names in the Prometheus datasource.

**Use cases:**
- Discover available metrics
- Find metrics by pattern
- Build metric explorers

#### List Prometheus Label Names
List all label names, optionally filtered by a metric selector.

**Use cases:**
- Discover labels for a metric
- Build dynamic label filters
- Understand metric dimensions

#### List Prometheus Label Values
List all values for a specific label name, optionally filtered by a metric selector.

**Use cases:**
- Get all pod names
- List all namespaces
- Discover label value ranges

## Loki Querying

Query logs and retrieve metadata from Loki datasources using LogQL.

### Available Operations

#### Query Loki Logs
Execute LogQL queries with support for:
- **Log queries** - Retrieve log lines
- **Metric queries** - Calculate metrics from logs

**LogQL features:**
- Stream selector queries
- Log pipeline operations
- Label filtering
- Metric aggregations

**Use cases:**
- Search application logs
- Filter logs by labels
- Calculate error rates from logs
- Analyze log patterns

#### List Loki Label Names
Get all available label names in the log streams.

**Use cases:**
- Discover log labels
- Build dynamic log filters
- Understand log structure

#### List Loki Label Values
Get all values for a specific log label.

**Use cases:**
- Find all application names
- List all log levels
- Discover label cardinality

#### Query Loki Stats
Get statistics about log streams including:
- Stream count
- Chunk count
- Total bytes
- Entries count

**Use cases:**
- Understand log volume
- Monitor log ingestion
- Analyze storage usage

## Pyroscope Profiling

Query continuous profiling data from Pyroscope datasources.

### Available Operations

#### List Pyroscope Label Names
List label names that can be used to filter profiles.

**Use cases:**
- Discover available profile labels
- Build profile filters
- Understand profile dimensions

#### List Pyroscope Label Values
List values for a specific label name, useful for filtering profiles.

**Use cases:**
- Get all service names
- List all profile types
- Discover label values

#### List Pyroscope Profile Types
List available profile types (e.g., cpu, memory, goroutines).

**Use cases:**
- Discover available profile types
- Select appropriate profile for analysis
- Understand profiling coverage

#### Fetch Pyroscope Profile
Fetch a profile in DOT format for analysis and visualization.

**Use cases:**
- Retrieve CPU profiles
- Analyze memory allocations
- Investigate performance issues
- Generate flame graphs

## Incidents

Manage incidents in Grafana Incident with full CRUD capabilities.

### Available Operations

#### List Incidents
Search and list incidents with filtering options:
- Status (active, resolved, etc.)
- Severity
- Labels
- Time range

**Use cases:**
- View active incidents
- Search historical incidents
- Monitor incident trends

#### Create Incident
Create new incidents programmatically with:
- Title and description
- Severity level
- Labels
- Affected services

**Use cases:**
- Automated incident creation
- Integration with external systems
- Proactive incident reporting

#### Add Activity to Incident
Add activity items to existing incidents:
- Notes and updates
- Status changes
- Investigation findings

**Use cases:**
- Document investigation progress
- Add context to incidents
- Track resolution steps

#### Get Incident
Retrieve detailed information about a specific incident by ID.

**Use cases:**
- View incident details
- Check incident status
- Review incident history

> **Note:** Incident tools require basic Grafana roles (Viewer for read operations, Editor for write operations) instead of fine-grained RBAC permissions.

## Sift Investigations

Use Grafana Sift for automated issue detection and investigation.

### Available Operations

#### List Sift Investigations
Retrieve a list of Sift investigations with optional limit parameter.

**Use cases:**
- View recent investigations
- Track investigation history
- Monitor automated analysis

#### Get Sift Investigation
Retrieve detailed information about a specific investigation by UUID.

**Use cases:**
- Review investigation findings
- Check investigation status
- Analyze detected patterns

#### Get Sift Analysis
Retrieve a specific analysis from a Sift investigation.

**Use cases:**
- Deep dive into specific findings
- Review analysis details
- Extract analysis data

#### Find Error Pattern Logs
Automatically detect elevated error patterns in Loki logs using Sift's pattern detection.

**Use cases:**
- Detect unusual error spikes
- Identify new error patterns
- Automated error analysis

#### Find Slow Requests
Detect slow requests from Tempo datasources using Sift analysis.

**Use cases:**
- Identify performance degradation
- Find slow endpoints
- Analyze latency issues

> **Note:** Sift tools require basic Grafana roles (Viewer for read operations, Editor for write operations).

## Alerting

Manage Grafana alerting rules and notification contact points.

### Available Operations

#### List Alert Rules
View all alert rules with their current states:
- Firing
- Normal
- Pending
- Error
- No data

**Use cases:**
- Monitor alert status
- Audit alert configuration
- Check alert health

#### Get Alert Rule by UID
Retrieve detailed configuration for a specific alert rule.

**Use cases:**
- Review alert conditions
- Check notification settings
- Verify alert configuration

#### List Contact Points
View all configured notification contact points including:
- Email
- Slack
- PagerDuty
- Webhook
- And more

**Use cases:**
- Audit notification configuration
- Verify contact point settings
- Discover available notification channels

## Grafana OnCall

Manage on-call schedules and view alert groups in Grafana OnCall.

### Available Operations

#### List OnCall Schedules
View all on-call schedules in your organization.

**Use cases:**
- Discover available schedules
- Audit schedule configuration
- Check schedule coverage

#### Get OnCall Shift
Retrieve detailed information about a specific on-call shift.

**Use cases:**
- View shift details
- Check shift assignments
- Verify shift timing

#### Get Current OnCall Users
See which users are currently on call for a specific schedule.

**Use cases:**
- Find who's on call right now
- Check coverage
- Contact on-call team

#### List OnCall Teams
View all teams configured in Grafana OnCall.

**Use cases:**
- Discover team structure
- Audit team configuration
- Check team membership

#### List OnCall Users
View all users configured in Grafana OnCall.

**Use cases:**
- User discovery
- Contact information lookup
- Team membership verification

#### List Alert Groups
View and filter alert groups from Grafana OnCall by:
- State (new, acknowledged, resolved, etc.)
- Integration
- Labels
- Time range

**Use cases:**
- Monitor active alerts
- Review alert history
- Track alert resolution

#### Get Alert Group
Retrieve detailed information about a specific alert group by ID.

**Use cases:**
- Review alert details
- Check acknowledgment status
- View alert timeline

## Admin

Administrative operations for managing teams and users.

### Available Operations

#### List Teams
View all teams configured in your Grafana organization.

**Use cases:**
- Audit team structure
- Discover team names
- Check team configuration

#### List Users by Organization
View all users in a Grafana organization.

**Use cases:**
- User auditing
- Access management
- User discovery

## Navigation

Generate accurate deeplink URLs to Grafana resources instead of relying on AI URL guessing.

### Available Link Types

#### Dashboard Links
Generate direct links to dashboards using their UID:
```
http://localhost:3000/d/dashboard-uid
```

#### Panel Links
Create links to specific panels within dashboards:
```
http://localhost:3000/d/dashboard-uid?viewPanel=5
```

#### Explore Links
Generate links to Grafana Explore with pre-configured datasources:
```
http://localhost:3000/explore?left={"datasource":"prometheus-uid"}
```

#### Time Range Support
Add time range parameters to any link:
```
?from=now-1h&to=now
```

#### Custom Parameters
Include additional query parameters:
- Dashboard variables
- Refresh intervals
- View modes
- And more

**Use cases:**
- Share specific dashboards
- Link directly to panels
- Create bookmarks with time ranges
- Generate Explore URLs with pre-filled queries

## Asserts

Integration with the Grafana Asserts plugin for observability assertions.

### Available Operations

#### Get Assertions
Retrieve assertion summaries for a given entity (service, pod, etc.).

**Use cases:**
- Check service health assertions
- Review assertion violations
- Monitor service quality

> **Note:** Asserts tools require plugin-specific permissions and scopes.

## Feature Configuration

Control which features are available using CLI flags. See [Configuration Guide](CONFIGURATION.md#tool-configuration) for details.

### Why Configure Features?

- **Reduce context window usage** - Disable unused features to minimize AI token consumption
- **Security** - Limit access to specific Grafana capabilities
- **Performance** - Skip initialization of unused clients
- **Simplicity** - Focus on relevant use cases

## Next Steps

- [View all available tools](TOOLS_REFERENCE.md)
- [Configure RBAC permissions](RBAC.md)
- [Set up your MCP client](CONFIGURATION.md)
- [Review examples](examples/)