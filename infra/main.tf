resource "kubernetes_namespace" "mcp" {
  metadata {
    name = var.namespace
  }
}

resource "helm_release" "mcp_grafana" {
  name       = "mcp-grafana"
  namespace  = kubernetes_namespace.mcp.metadata[0].name
  repository = "https://grafana.github.io/helm-charts"
  chart      = "grafana-mcp"

  set {
    name  = "grafana.url"
    value = var.grafana_url
  }

  set {
    name  = "grafana.apiKey"
    value = var.grafana_api_key
  }

  set {
    name  = "image.tag"
    value = var.image_tag
  }
}
