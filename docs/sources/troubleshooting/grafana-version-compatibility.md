---
title: Grafana version compatibility
menuTitle: Grafana version
description: Datasource errors on Grafana versions earlier than 9.0.
keywords:
  - Grafana
  - compatibility
  - upgrade
weight: 1
aliases: []
---

# Grafana version compatibility

Some datasource API paths exist only in newer Grafana releases. This article explains a common error on Grafana before 9.0.

## What you'll achieve

You can confirm whether an upgrade fixes datasource-related MCP errors.

## Before you begin

- Access to upgrade or validate the Grafana version for your stack.

## Recognize the error

If you see an error like this when using datasource-related tools:

```
get datasource by uid : [GET /datasources/uid/{uid}][400] getDataSourceByUidBadRequest {"message":"id is invalid"}
```

you are likely on **Grafana before 9.0**. The `/datasources/uid/{uid}` API was added in Grafana 9.0; datasource operations that rely on it fail on older versions.

## Resolve the issue

Upgrade Grafana to **9.0 or later**. The MCP server [requires Grafana 9.0 or later](../../introduction/) for full functionality.

## Next steps

- [Introduction](../../introduction/)
- [Client configuration examples](../../set-up/client-configuration-examples/)
