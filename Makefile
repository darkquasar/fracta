.PHONY: build build-all test install clean \
       spike-build spike-load spike-test \
       docker-build docker-load \
       compose-up compose-up-op compose-down compose-logs compose-ps \
       vendor-mcp-build vendor-mcp-load \
       k8s-deploy k8s-deploy-mcp k8s-deploy-gateway k8s-deploy-controlplane k8s-teardown \
       k8s-secrets k8s-refresh-auth k8s-setup k8s-status k8s-smoke \
       k8s-ensure-image k8s-spike

# IMPORTANT: Must build to bin/fracta — .mcp.json points Claude Code to this location
build:
	go build -o bin/fracta .

build-all: build

test:
	go test ./...

install:
	$(MAKE) build-all

clean:
	rm -rf bin/

# --- Docker targets ---

DOCKER_IMAGE ?= fracta/agent
DOCKER_TAG ?= latest
COMPOSE_FILE ?= deployment/docker-compose/docker-compose.yml
K8S_IMAGE_LOADER ?= docker-desktop
KIND_CLUSTER ?= kind
MINIKUBE_PROFILE ?= minikube
K3D_CLUSTER ?= fracta

define load_k8s_image
	@if [ "$(K8S_IMAGE_LOADER)" = "docker-desktop" ]; then \
	  docker save $(1) | docker exec -i desktop-control-plane ctr -n k8s.io images import -; \
	elif [ "$(K8S_IMAGE_LOADER)" = "kind" ]; then \
	  kind load docker-image $(1) --name $(KIND_CLUSTER); \
	elif [ "$(K8S_IMAGE_LOADER)" = "minikube" ]; then \
	  minikube image load $(1) -p $(MINIKUBE_PROFILE); \
	elif [ "$(K8S_IMAGE_LOADER)" = "k3d" ]; then \
	  k3d image import $(1) -c $(K3D_CLUSTER); \
	else \
	  echo "Unsupported K8S_IMAGE_LOADER=$(K8S_IMAGE_LOADER). Use docker-desktop, kind, minikube, or k3d."; \
	  exit 1; \
	fi
endef

docker-build:
	docker build -t $(DOCKER_IMAGE):$(DOCKER_TAG) .

docker-load:
	$(call load_k8s_image,$(DOCKER_IMAGE):$(DOCKER_TAG))

compose-up:
	docker compose -f $(COMPOSE_FILE) up -d

compose-up-op:
	op run --env-file .op-hunt-env -- docker compose -f $(COMPOSE_FILE) up -d

compose-down:
	docker compose -f $(COMPOSE_FILE) down

compose-logs:
	docker compose -f $(COMPOSE_FILE) logs -f

compose-ps:
	docker compose -f $(COMPOSE_FILE) ps

VENDOR_MCP_IMAGE ?= fracta/vendor-mcp
VENDOR_MCP_TAG ?= latest

vendor-mcp-build:
	docker build -f deployment/mcp-servers/vendor/Dockerfile -t $(VENDOR_MCP_IMAGE):$(VENDOR_MCP_TAG) .

vendor-mcp-load:
	$(call load_k8s_image,$(VENDOR_MCP_IMAGE):$(VENDOR_MCP_TAG))

# --- K8s targets ---

k8s-deploy:
	kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/workspace-pvc.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/rbac.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/falkordb.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/postgres.yaml

k8s-deploy-mcp:
	kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/elastic-mcp.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/vendor-mcp.yaml

k8s-deploy-gateway:
	kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/postgres.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/fracta-gateway.yaml

k8s-deploy-controlplane:
	kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/postgres.yaml
	kubectl apply -f deployment/k8s-local-cluster/manifests/fracta-controlplane.yaml

k8s-teardown:
	kubectl delete namespace fracta --ignore-not-found
	kubectl delete pv fracta-workspace-pv --ignore-not-found

k8s-secrets:
	@echo "Creating secrets..."
	kubectl create secret generic postgres-secrets \
	  --namespace fracta \
	  --from-literal=password=fracta-dev-password \
	  --dry-run=client -o yaml | kubectl apply -f -
	@echo "Creating MCP secrets from 1Password..."
	op run --env-file .op-hunt-env -- sh -c '\
	  kubectl create secret generic elastic-mcp-secrets \
	    --namespace fracta \
	    --from-literal=url="$$ELASTIC_URL" \
	    --from-literal=api-key="$$ELASTIC_API_KEY" \
	    --dry-run=client -o yaml | kubectl apply -f - && \
	  kubectl create secret generic vendor-mcp-secrets \
	    --namespace fracta \
	    --from-literal=console-base-url="$$VENDOR_MCP_CONSOLE_BASE_URL" \
	    --from-literal=console-token="$$VENDOR_MCP_CONSOLE_TOKEN" \
	    --dry-run=client -o yaml | kubectl apply -f -'

k8s-refresh-auth:
	kubectl create secret generic fracta-auth \
	  --namespace fracta \
	  --from-literal=bearer-token="$$(bedrock-auth-helper)" \
	  --dry-run=client -o yaml | kubectl apply -f -

k8s-setup: docker-build docker-load vendor-mcp-build vendor-mcp-load k8s-deploy k8s-deploy-mcp k8s-deploy-gateway k8s-deploy-controlplane k8s-secrets
	@echo "Full K8s local setup complete"

k8s-status:
	@echo "=== Pods ==="
	kubectl get pods -n fracta
	@echo "=== Deployments ==="
	kubectl get deployments -n fracta
	@echo "=== Services ==="
	kubectl get svc -n fracta
	@echo "=== StatefulSets ==="
	kubectl get statefulsets -n fracta
	@echo "=== Jobs ==="
	kubectl get jobs -n fracta
	@echo "=== PVCs ==="
	kubectl get pvc -n fracta
	@echo "=== Secrets ==="
	kubectl get secrets -n fracta

k8s-smoke:
	scripts/k8s-smoke-test.sh

# --- Spike targets (legacy, from Phase 0) ---

spike-build:
	docker build -f Dockerfile.spike -t fracta/spike .

spike-load:
	$(call load_k8s_image,fracta/spike:latest)

spike-test:
	docker run --rm \
	  -e BEDROCK_BEARER_TOKEN=$$(bedrock-auth-helper) \
	  -e ANTHROPIC_MODEL=global.anthropic.claude-haiku-4-5-20251001-v1:0 \
	  fracta/spike -p "Respond with only the word 'pong'" --output-format json --permission-mode dontAsk

# --- K8s agent targets ---

k8s-ensure-image:
	@if [ "$(K8S_IMAGE_LOADER)" = "docker-desktop" ] && \
	    docker exec $$(kubectl get nodes -o jsonpath='{.items[0].metadata.name}') \
	    crictl images 2>/dev/null | grep -q "$(DOCKER_IMAGE)"; then \
	  echo "Image already loaded"; \
	else \
	  $(MAKE) docker-build docker-load; \
	fi

k8s-spike: k8s-ensure-image
	kubectl apply -f deployment/k8s-local-cluster/manifests/namespace.yaml
	kubectl delete job spike-agent-ping -n fracta --ignore-not-found
	kubectl apply -f deployment/k8s-local-cluster/manifests/k8s-spike.yaml
	kubectl wait --for=condition=complete job/spike-agent-ping -n fracta --timeout=120s
	kubectl logs job/spike-agent-ping -n fracta
