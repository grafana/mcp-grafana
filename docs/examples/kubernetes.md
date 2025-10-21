# Kubernetes Configuration Examples

This guide provides comprehensive Kubernetes deployment examples for the Grafana MCP server.

## Table of Contents

- [Prerequisites](#prerequisites)
- [Helm Deployment](#helm-deployment)
- [Manual Deployment](#manual-deployment)
- [Configuration Management](#configuration-management)
- [Service Configuration](#service-configuration)
- [Ingress Configuration](#ingress-configuration)
- [Production Deployment](#production-deployment)
- [Troubleshooting](#troubleshooting)

## Prerequisites

- Kubernetes cluster (1.19+)
- `kubectl` configured
- Helm 3.x (for Helm deployments)
- Grafana instance accessible from cluster

## Helm Deployment

### Basic Helm Installation

The easiest way to deploy to Kubernetes is using the official Helm chart:

```bash
# Add Grafana Helm repository
helm repo add grafana https://grafana.github.io/helm-charts
helm repo update

# Install with basic configuration
helm install mcp-grafana grafana/grafana-mcp \
  --set grafana.url=https://myinstance.grafana.net \
  --set grafana.apiKey=<your-service-account-token>
```

### Helm with Custom Values

Create a `values.yaml` file:

```yaml
# values.yaml
grafana:
  url: "https://myinstance.grafana.net"
  apiKey: "<your-service-account-token>"

# Service configuration
service:
  type: LoadBalancer
  port: 8000
  annotations:
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"

# Resource limits
resources:
  limits:
    cpu: 500m
    memory: 512Mi
  requests:
    cpu: 100m
    memory: 128Mi

# Replica count
replicaCount: 2

# Health checks
livenessProbe:
  enabled: true
  httpGet:
    path: /healthz
    port: 8000
  initialDelaySeconds: 10
  periodSeconds: 30

readinessProbe:
  enabled: true
  httpGet:
    path: /healthz
    port: 8000
  initialDelaySeconds: 5
  periodSeconds: 10

# Autoscaling
autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 10
  targetCPUUtilizationPercentage: 80
```

Install with custom values:

```bash
helm install mcp-grafana grafana/grafana-mcp -f values.yaml
```

### Helm Management Commands

```bash
# Upgrade deployment
helm upgrade mcp-grafana grafana/grafana-mcp -f values.yaml

# Check status
helm status mcp-grafana

# Get values
helm get values mcp-grafana

# Rollback
helm rollback mcp-grafana

# Uninstall
helm uninstall mcp-grafana
```

## Manual Deployment

### Basic Deployment

**Namespace:**

```yaml
# namespace.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: mcp-grafana
```

**Secret for Service Account Token:**

```yaml
# secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: grafana-credentials
  namespace: mcp-grafana
type: Opaque
stringData:
  service-account-token: "<your-service-account-token>"
  grafana-url: "https://myinstance.grafana.net"
```

**Deployment:**

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  labels:
    app: mcp-grafana
spec:
  replicas: 2
  selector:
    matchLabels:
      app: mcp-grafana
  template:
    metadata:
      labels:
        app: mcp-grafana
    spec:
      containers:
      - name: mcp-grafana
        image: mcp/grafana:latest
        ports:
        - containerPort: 8000
          name: http
          protocol: TCP
        env:
        - name: GRAFANA_URL
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: grafana-url
        - name: GRAFANA_SERVICE_ACCOUNT_TOKEN
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: service-account-token
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8000
          initialDelaySeconds: 10
          periodSeconds: 30
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8000
          initialDelaySeconds: 5
          periodSeconds: 10
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 100m
            memory: 128Mi
```

**Service:**

```yaml
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  labels:
    app: mcp-grafana
spec:
  type: LoadBalancer
  ports:
  - port: 8000
    targetPort: 8000
    protocol: TCP
    name: http
  selector:
    app: mcp-grafana
```

**Apply manifests:**

```bash
kubectl apply -f namespace.yaml
kubectl apply -f secret.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
```

### With ConfigMap

**ConfigMap for additional configuration:**

```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: mcp-grafana-config
  namespace: mcp-grafana
data:
  DISABLE_ONCALL: "true"
  DISABLE_INCIDENT: "true"
  DISABLE_SIFT: "true"
```

**Deployment using ConfigMap:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  replicas: 2
  selector:
    matchLabels:
      app: mcp-grafana
  template:
    metadata:
      labels:
        app: mcp-grafana
    spec:
      containers:
      - name: mcp-grafana
        image: mcp/grafana:latest
        args:
        - "--disable-oncall"
        - "--disable-incident"
        - "--disable-sift"
        ports:
        - containerPort: 8000
        env:
        - name: GRAFANA_URL
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: grafana-url
        - name: GRAFANA_SERVICE_ACCOUNT_TOKEN
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: service-account-token
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 100m
            memory: 128Mi
```

## Configuration Management

### Using Secrets for TLS Certificates

**Create secret from files:**

```bash
kubectl create secret generic mcp-tls-certs \
  --from-file=client.crt=/path/to/client.crt \
  --from-file=client.key=/path/to/client.key \
  --from-file=ca.crt=/path/to/ca.crt \
  --namespace=mcp-grafana
```

**Deployment with TLS:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  replicas: 2
  selector:
    matchLabels:
      app: mcp-grafana
  template:
    metadata:
      labels:
        app: mcp-grafana
    spec:
      containers:
      - name: mcp-grafana
        image: mcp/grafana:latest
        args:
        - "--tls-cert-file"
        - "/certs/client.crt"
        - "--tls-key-file"
        - "/certs/client.key"
        - "--tls-ca-file"
        - "/certs/ca.crt"
        volumeMounts:
        - name: tls-certs
          mountPath: /certs
          readOnly: true
        env:
        - name: GRAFANA_URL
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: grafana-url
        - name: GRAFANA_SERVICE_ACCOUNT_TOKEN
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: service-account-token
      volumes:
      - name: tls-certs
        secret:
          secretName: mcp-tls-certs
```

### Using External Secrets Operator

For managing secrets from external sources (AWS Secrets Manager, HashiCorp Vault, etc.):

```yaml
# external-secret.yaml
apiVersion: external-secrets.io/v1beta1
kind: ExternalSecret
metadata:
  name: grafana-credentials
  namespace: mcp-grafana
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: aws-secretsmanager
    kind: SecretStore
  target:
    name: grafana-credentials
    creationPolicy: Owner
  data:
  - secretKey: service-account-token
    remoteRef:
      key: prod/grafana/mcp-token
  - secretKey: grafana-url
    remoteRef:
      key: prod/grafana/url
```

## Service Configuration

### ClusterIP Service

For internal cluster access only:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  type: ClusterIP
  ports:
  - port: 8000
    targetPort: 8000
    protocol: TCP
  selector:
    app: mcp-grafana
```

### NodePort Service

For access via node IP:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  type: NodePort
  ports:
  - port: 8000
    targetPort: 8000
    nodePort: 30800
    protocol: TCP
  selector:
    app: mcp-grafana
```

### LoadBalancer Service

For external access with cloud load balancer:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  annotations:
    # AWS
    service.beta.kubernetes.io/aws-load-balancer-type: "nlb"
    # GCP
    cloud.google.com/load-balancer-type: "Internal"
    # Azure
    service.beta.kubernetes.io/azure-load-balancer-internal: "true"
spec:
  type: LoadBalancer
  ports:
  - port: 8000
    targetPort: 8000
    protocol: TCP
  selector:
    app: mcp-grafana
```

## Ingress Configuration

### NGINX Ingress

```yaml
# ingress.yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  annotations:
    kubernetes.io/ingress.class: "nginx"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
    nginx.ingress.kubernetes.io/ssl-redirect: "true"
spec:
  tls:
  - hosts:
    - mcp.example.com
    secretName: mcp-grafana-tls
  rules:
  - host: mcp.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: mcp-grafana
            port:
              number: 8000
```

### Traefik Ingress

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  annotations:
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    cert-manager.io/cluster-issuer: "letsencrypt-prod"
spec:
  tls:
  - hosts:
    - mcp.example.com
    secretName: mcp-grafana-tls
  rules:
  - host: mcp.example.com
    http:
      paths:
      - path: /
        pathType: Prefix
        backend:
          service:
            name: mcp-grafana
            port:
              number: 8000
```

### With Path-Based Routing

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  annotations:
    kubernetes.io/ingress.class: "nginx"
    nginx.ingress.kubernetes.io/rewrite-target: /$2
spec:
  rules:
  - host: api.example.com
    http:
      paths:
      - path: /mcp(/|$)(.*)
        pathType: Prefix
        backend:
          service:
            name: mcp-grafana
            port:
              number: 8000
```

## Production Deployment

### With Horizontal Pod Autoscaler

```yaml
# hpa.yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: mcp-grafana
  minReplicas: 2
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 80
  - type: Resource
    resource:
      name: memory
      target:
        type: Utilization
        averageUtilization: 80
```

### With Pod Disruption Budget

```yaml
# pdb.yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  minAvailable: 1
  selector:
    matchLabels:
      app: mcp-grafana
```

### With Network Policies

```yaml
# network-policy.yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  podSelector:
    matchLabels:
      app: mcp-grafana
  policyTypes:
  - Ingress
  - Egress
  ingress:
  - from:
    - namespaceSelector:
        matchLabels:
          name: ingress-nginx
    ports:
    - protocol: TCP
      port: 8000
  egress:
  - to:
    - namespaceSelector: {}
    ports:
    - protocol: TCP
      port: 443  # HTTPS to Grafana
    - protocol: TCP
      port: 53   # DNS
    - protocol: UDP
      port: 53   # DNS
```

### With Resource Quotas

```yaml
# resource-quota.yaml
apiVersion: v1
kind: ResourceQuota
metadata:
  name: mcp-grafana-quota
  namespace: mcp-grafana
spec:
  hard:
    requests.cpu: "4"
    requests.memory: 8Gi
    limits.cpu: "8"
    limits.memory: 16Gi
    persistentvolumeclaims: "0"
    services.loadbalancers: "1"
```

### With Pod Security Policy

```yaml
# psp.yaml
apiVersion: policy/v1beta1
kind: PodSecurityPolicy
metadata:
  name: mcp-grafana
spec:
  privileged: false
  allowPrivilegeEscalation: false
  requiredDropCapabilities:
    - ALL
  volumes:
    - 'configMap'
    - 'emptyDir'
    - 'projected'
    - 'secret'
    - 'downwardAPI'
  hostNetwork: false
  hostIPC: false
  hostPID: false
  runAsUser:
    rule: 'MustRunAsNonRoot'
  seLinux:
    rule: 'RunAsAny'
  fsGroup:
    rule: 'RunAsAny'
  readOnlyRootFilesystem: false
```

### Complete Production Example

```yaml
---
apiVersion: v1
kind: Namespace
metadata:
  name: mcp-grafana
  labels:
    name: mcp-grafana

---
apiVersion: v1
kind: Secret
metadata:
  name: grafana-credentials
  namespace: mcp-grafana
type: Opaque
stringData:
  service-account-token: "<your-service-account-token>"
  grafana-url: "https://myinstance.grafana.net"

---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  labels:
    app: mcp-grafana
    version: v1.0.0
spec:
  replicas: 3
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  selector:
    matchLabels:
      app: mcp-grafana
  template:
    metadata:
      labels:
        app: mcp-grafana
        version: v1.0.0
      annotations:
        prometheus.io/scrape: "true"
        prometheus.io/port: "8000"
        prometheus.io/path: "/metrics"
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 1000
        fsGroup: 1000
      containers:
      - name: mcp-grafana
        image: mcp/grafana:v1.0.0
        imagePullPolicy: IfNotPresent
        ports:
        - containerPort: 8000
          name: http
          protocol: TCP
        env:
        - name: GRAFANA_URL
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: grafana-url
        - name: GRAFANA_SERVICE_ACCOUNT_TOKEN
          valueFrom:
            secretKeyRef:
              name: grafana-credentials
              key: service-account-token
        livenessProbe:
          httpGet:
            path: /healthz
            port: 8000
          initialDelaySeconds: 10
          periodSeconds: 30
          timeoutSeconds: 5
          failureThreshold: 3
        readinessProbe:
          httpGet:
            path: /healthz
            port: 8000
          initialDelaySeconds: 5
          periodSeconds: 10
          timeoutSeconds: 3
          failureThreshold: 3
        resources:
          limits:
            cpu: 500m
            memory: 512Mi
          requests:
            cpu: 100m
            memory: 128Mi
        securityContext:
          allowPrivilegeEscalation: false
          readOnlyRootFilesystem: true
          runAsNonRoot: true
          capabilities:
            drop:
            - ALL

---
apiVersion: v1
kind: Service
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
  labels:
    app: mcp-grafana
spec:
  type: ClusterIP
  ports:
  - port: 8000
    targetPort: 8000
    protocol: TCP
    name: http
  selector:
    app: mcp-grafana

---
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: mcp-grafana
  minReplicas: 3
  maxReplicas: 10
  metrics:
  - type: Resource
    resource:
      name: cpu
      target:
        type: Utilization
        averageUtilization: 80

---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: mcp-grafana
  namespace: mcp-grafana
spec:
  minAvailable: 2
  selector:
    matchLabels:
      app: mcp-grafana
```

## Troubleshooting

### Check Deployment Status

```bash
# Check pods
kubectl get pods -n mcp-grafana

# Describe pod
kubectl describe pod <pod-name> -n mcp-grafana

# Check logs
kubectl logs <pod-name> -n mcp-grafana

# Follow logs
kubectl logs -f <pod-name> -n mcp-grafana

# Previous container logs
kubectl logs <pod-name> -n mcp-grafana --previous
```

### Check Service and Endpoints

```bash
# Check service
kubectl get svc -n mcp-grafana

# Check endpoints
kubectl get endpoints -n mcp-grafana

# Describe service
kubectl describe svc mcp-grafana -n mcp-grafana
```

### Test Connectivity

```bash
# Port forward to local machine
kubectl port-forward -n mcp-grafana svc/mcp-grafana 8000:8000

# Test from local machine
curl http://localhost:8000/healthz

# Execute command in pod
kubectl exec -it <pod-name> -n mcp-grafana -- curl localhost:8000/healthz

# Run debug pod
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -n mcp-grafana -- sh
# Then from inside: curl http://mcp-grafana:8000/healthz
```

### Check Secrets and ConfigMaps

```bash
# List secrets
kubectl get secrets -n mcp-grafana

# Describe secret (doesn't show values)
kubectl describe secret grafana-credentials -n mcp-grafana

# View secret value (use carefully)
kubectl get secret grafana-credentials -n mcp-grafana -o jsonpath='{.data.service-account-token}' | base64 -d

# List configmaps
kubectl get configmaps -n mcp-grafana
```

### Debug Common Issues

**Pods not starting:**

```bash
# Check events
kubectl get events -n mcp-grafana --sort-by='.lastTimestamp'

# Check pod status
kubectl get pod <pod-name> -n mcp-grafana -o yaml

# Check if image can be pulled
kubectl describe pod <pod-name> -n mcp-grafana | grep -A 5 "Events:"
```

**Connection issues:**

```bash
# Test DNS resolution
kubectl run -it --rm debug --image=busybox --restart=Never -- nslookup mcp-grafana.mcp-grafana.svc.cluster.local

# Check network policies
kubectl get networkpolicies -n mcp-grafana

# Test connectivity to Grafana
kubectl run -it --rm debug --image=curlimages/curl --restart=Never -- curl -v <grafana-url>/api/health
```

## Best Practices

1. **Use namespaces** to isolate MCP server resources
2. **Store credentials in Secrets** never in ConfigMaps or code
3. **Set resource limits** to prevent resource exhaustion
4. **Enable health checks** for proper pod lifecycle management
5. **Use HPA** for automatic scaling based on load
6. **Configure PDB** to maintain availability during updates
7. **Use specific image tags** not `latest` in production
8. **Enable RBAC** and follow least privilege principle
9. **Use network policies** to restrict traffic
10. **Monitor and log** with Prometheus and centralized logging
11. **Regular updates** keep images and charts up to date
12. **Test in staging** before deploying to production

## Next Steps

- [Configure TLS certificates](../CONFIGURATION.md#tls-configuration)
- [Set up RBAC permissions](../RBAC.md)
- [Review monitoring setup](../FEATURES.md)
- [Check troubleshooting guide](../TROUBLESHOOTING.md)