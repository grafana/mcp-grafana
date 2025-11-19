variable "namespace" {
  type    = string
  default = "mcp-grafana"
}

variable "grafana_url" {
  type        = string
  description = "https://grafana.ops.aws.abcfinancial.net/"
}

variable "grafana_api_key" {
  type        = string
  description = "Grafana API key"
  sensitive   = true
}

variable "image_repository" {
  type        = string
  description = "Container image repository for the MCP server"
  default     = "mcp/grafana"
}

variable "image_tag" {
  type        = string
  description = "Image tag to deploy"
  default     = "latest"
}
