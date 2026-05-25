.PHONY: build build-all test install clean \
       spike-build spike-load spike-test \
       docker-build docker-load \
       vendor-mcp-build vendor-mcp-load \
       mcp-build-all mcp-load-all \
       k8s-teardown \
       k8s-secrets k8s-refresh-auth k8s-status k8s-smoke \
       k8s-ensure-image \
       docs-gen docs-gen-check \
       verify-bindings lint-strategies

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

# --- MCP server images (auto-discovered) ---
#
# Every `mcp-servers/<vendor>/[<server>/]Dockerfile` becomes a pair of
# targets `mcp-build/<key>` and `mcp-load/<key>`, where <key> is the path
# under `mcp-servers/` with `/` replaced by `-`.
#
#   mcp-servers/vendor/Dockerfile                    -> key: vendor
#   mcp-servers/fracta/fracta-test-server/Dockerfile -> key: fracta-fracta-test-server
#
# Image name: $(MCP_IMAGE_PREFIX)-<key>:$(MCP_IMAGE_TAG).
# Build context is the directory containing the Dockerfile, so each server
# can `COPY` its own files without leaking the rest of the repo.

MCP_IMAGE_PREFIX ?= fracta/mcp
MCP_IMAGE_TAG ?= latest
MCP_DOCKERFILES := $(shell find mcp-servers -name Dockerfile 2>/dev/null)

mcp_image_key = $(subst /,-,$(patsubst mcp-servers/%/Dockerfile,%,$(1)))

define mcp_image_rules
mcp-build/$(call mcp_image_key,$(1)):
	docker build -f $(1) -t $(MCP_IMAGE_PREFIX)-$(call mcp_image_key,$(1)):$(MCP_IMAGE_TAG) $(dir $(1))

mcp-load/$(call mcp_image_key,$(1)):
	$$(call load_k8s_image,$(MCP_IMAGE_PREFIX)-$(call mcp_image_key,$(1)):$(MCP_IMAGE_TAG))

.PHONY: mcp-build/$(call mcp_image_key,$(1)) mcp-load/$(call mcp_image_key,$(1))
endef

$(foreach df,$(MCP_DOCKERFILES),$(eval $(call mcp_image_rules,$(df))))

mcp-build-all: $(foreach df,$(MCP_DOCKERFILES),mcp-build/$(call mcp_image_key,$(df)))
mcp-load-all:  $(foreach df,$(MCP_DOCKERFILES),mcp-load/$(call mcp_image_key,$(df)))

# Backwards-compatible shims for the old `vendor-mcp-*` names (still
# referenced by k8s-secrets and external docs). They delegate to the
# auto-generated targets, which only exist when
# `mcp-servers/vendor/Dockerfile` is present.
VENDOR_MCP_IMAGE ?= $(MCP_IMAGE_PREFIX)-vendor
VENDOR_MCP_TAG ?= $(MCP_IMAGE_TAG)

vendor-mcp-build:
	@if [ ! -f mcp-servers/vendor/Dockerfile ]; then \
	  echo "vendor-mcp-build: mcp-servers/vendor/Dockerfile is missing — add a Dockerfile under mcp-servers/vendor/ or use one of: $(foreach df,$(MCP_DOCKERFILES),mcp-build/$(call mcp_image_key,$(df)))"; \
	  exit 1; \
	fi
	$(MAKE) mcp-build/vendor

vendor-mcp-load:
	@if [ ! -f mcp-servers/vendor/Dockerfile ]; then \
	  echo "vendor-mcp-load: mcp-servers/vendor/Dockerfile is missing"; \
	  exit 1; \
	fi
	$(MAKE) mcp-load/vendor

# --- K8s targets ---

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
	op run --env-file .op-env -- sh -c '\
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

# --- Strategy catalogue (autogenerated docs) ---
#
# Walks strategies/, reads contract.yaml + strategy.py + optional README.md
# for each, and writes one .mdx per strategy under docs/strategies/catalogue/.
# Authored by scripts/gen-strategy-catalogue.py — see that file for the
# security posture (safe_load + AST only, never imports strategy code).

docs-gen:
	python3 scripts/gen-strategy-catalogue.py

# Used by CI to detect "PR changed a strategy but forgot to regenerate."
# Exits non-zero if the generated tree would differ from what's committed.
docs-gen-check:
	python3 scripts/gen-strategy-catalogue.py --check

# --- Strategy lint (spec-50 §7.1, §7.2) ---
#
# verify-bindings walks every strategy's binding.yaml and asserts the
# referenced mcp_tool exists on its mcp_server (against recorded tools/list
# fixtures for hermetic CI runs, live for nightly). Closes Bug 5/6/7/8 +
# 16/17/18/19/23 regression class.
verify-bindings:
	python3 scripts/verify-bindings.py

# lint-strategies blocks `ctx.graph.execute(` from creeping back into any
# strategy. The FalkorDB SDK Graph object only exposes .query() — .execute()
# was hallucinated and led to Bugs 11/14/15. Cheap and effective.
lint-strategies:
	@hits=$$(grep -rn 'ctx\.graph\.execute(' strategies/ 2>/dev/null || true); \
	if [ -n "$$hits" ]; then \
	  echo "ERROR: ctx.graph.execute(...) is not a valid FalkorDB SDK call. Use ctx.graph.query(...) instead."; \
	  echo "$$hits"; \
	  exit 1; \
	fi
	@echo "lint-strategies: no ctx.graph.execute( hits"
