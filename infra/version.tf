terraform {
  required_version = "1.9.5"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.39"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.13"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.18"
    }
  }
}