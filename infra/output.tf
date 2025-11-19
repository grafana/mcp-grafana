output "namespace" {
  value = kubernetes_namespace.mcp.metadata[0].name
}

output "release_name" {
  value = helm_release.mcp_grafana.name
}
