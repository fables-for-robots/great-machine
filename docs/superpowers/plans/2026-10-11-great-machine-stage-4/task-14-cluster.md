### Task 14: On a cluster, by hand

Everything before this task was seen in one process, or in one container on a laptop. Four things were not, and each can only be seen on a cluster whose nodes have AppArmor: that a pod's container which is not privileged mounts with `CAP_SYS_ADMIN` and the profile `Unconfined`, and does not without the profile; that a pod leaves nothing mounted on its node; that the laptop and the pods both reach the bucket by the URLs draal signs; and that all of it works over iroh between a laptop and a cluster. **This task writes no code.** It is run by hand on a Linux machine, x86_64, with Docker, Nix, `kind` and `kubectl`, and what it finds goes into the ledger: every command below with what it printed where that is asked for, and every departure from "Expected".

If something here does not hold, stop and say so: the spec's §5.1 and `deploy/pod.yaml` then change before anything is merged.

**Files:**
- Modify: `docs/superpowers/plans/2026-10-11-great-machine-stage-4/ledger.md` (what was seen)
- Nothing else. The files under `/tmp/gm4` are thrown away at the end.

**Interfaces:**
- Consumes: the finished branch: the image (`nix build .#image`), `deploy/draal.yaml`, `deploy/pod.yaml`, the program on the machine's `PATH` or as `./result/bin/great-machine`.
- Produces: nothing that code relies on.

- [ ] **Step 1: The machine**

```bash
uname -m                                             # x86_64
cat /sys/module/apparmor/parameters/enabled          # Y
docker info --format '{{.SecurityOptions}}'          # names apparmor and seccomp
kind version && kubectl version --client
mkdir -p /tmp/gm4
```

Expected: AppArmor is enabled. On a machine without it this task shows nothing about the profile: say so in the ledger and go on, for the rest still counts.

- [ ] **Step 2: The program and the image**

From the root of the repository, on the branch:

```bash
nix build --option allow-unsafe-native-code-during-evaluation true -o /tmp/gm4/program
nix build .#image --option allow-unsafe-native-code-during-evaluation true -o /tmp/gm4/image
docker load < /tmp/gm4/image
export PATH=/tmp/gm4/program/bin:$PATH
great-machine version
docker run --rm ghcr.io/fables-for-robots/great-machine:nix version
```

Expected: both print the same commit. This is also the first build of the program by Nix for x86_64 Linux with the packages of the mount in it: note how long it took.

- [ ] **Step 3: A cluster, and a bucket in it**

```bash
kind create cluster --name gm4
kind load docker-image ghcr.io/fables-for-robots/great-machine:nix --name gm4
NODE=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' gm4-control-plane)
echo "$NODE"
docker exec gm4-control-plane sh -c 'cat /sys/module/apparmor/parameters/enabled; findmnt -t nfs4 | wc -l'
```

Expected: `Y`, and `0`.

The bucket is RustFS in the cluster, behind a NodePort, so that the laptop and the pods reach it under one name, the node's address:

```bash
cat > /tmp/gm4/rustfs.yaml <<'EOF'
apiVersion: apps/v1
kind: Deployment
metadata:
  name: rustfs
spec:
  replicas: 1
  selector:
    matchLabels: { app: rustfs }
  template:
    metadata:
      labels: { app: rustfs }
    spec:
      containers:
        - name: rustfs
          image: rustfs/rustfs:1.0.1
          env:
            - { name: RUSTFS_ACCESS_KEY, value: gm4access }
            - { name: RUSTFS_SECRET_KEY, value: gm4secret-gm4secret }
          ports:
            - containerPort: 9000
          readinessProbe:
            httpGet: { path: /health, port: 9000 }
---
apiVersion: v1
kind: Service
metadata:
  name: rustfs
spec:
  type: NodePort
  selector: { app: rustfs }
  ports:
    - { port: 9000, targetPort: 9000, nodePort: 30900 }
EOF
kubectl apply -f /tmp/gm4/rustfs.yaml
kubectl rollout status deployment/rustfs --timeout=180s
export AWS_ACCESS_KEY_ID=gm4access AWS_SECRET_ACCESS_KEY=gm4secret-gm4secret AWS_REGION=us-east-1
nix run nixpkgs#awscli2 -- --endpoint-url "http://$NODE:30900" s3 mb s3://gm4-packs
```

Expected: `make_bucket: gm4-packs`.

- [ ] **Step 4: draal**

```bash
great-machine --key /tmp/gm4/operator.key id > /tmp/gm4/operator.id
kubectl create secret generic draal-s3 \
  --from-literal=AWS_ACCESS_KEY_ID=gm4access \
  --from-literal=AWS_SECRET_ACCESS_KEY=gm4secret-gm4secret
sed -e "s|great-machine:v0.2.0|great-machine:nix|" \
    -e "s|2222222222222222222222222222222222222222222222222222222222222222|$(cat /tmp/gm4/operator.id)|" \
    -e "s|value: my-packs|value: gm4-packs|" \
    -e "s|value: eu-central-1|value: us-east-1|" \
    -e "s|# - name: GREAT_MACHINE_S3_ENDPOINT|- name: GREAT_MACHINE_S3_ENDPOINT|" \
    -e "s|#   value: https://s3.example.com|  value: http://$NODE:30900|" \
    -e "s|# - name: GREAT_MACHINE_S3_PATH_STYLE|- name: GREAT_MACHINE_S3_PATH_STYLE|" \
    -e "s|#   value: \"true\"|  value: \"true\"|" \
    deploy/draal.yaml > /tmp/gm4/draal.yaml
grep -n -A1 "S3_" /tmp/gm4/draal.yaml | grep -v '#'
kubectl apply -f /tmp/gm4/draal.yaml
kubectl rollout status deployment/draal --timeout=180s
kubectl logs deployment/draal | grep -i endpoint
export GREAT_MACHINE_DRAAL=$(kubectl logs deployment/draal | sed -n 's/.*msg=serving.*endpoint=\([0-9a-f]\{64\}\).*/\1/p' | head -1)
export GREAT_MACHINE_KEY=/tmp/gm4/operator.key
echo "$GREAT_MACHINE_DRAAL"
great-machine status
```

The image `:nix` is in the node and in no registry: if the pod waits with `ErrImagePull`, add `imagePullPolicy: Never` to the container in `/tmp/gm4/draal.yaml` and in the pod's file below, and note it.

Expected: the `grep` shows the bucket, the region `us-east-1`, the endpoint `http://` and the node's address with `:30900`, and the path style; draal logs `serving` with its endpoint ID and the bucket; and `great-machine status` answers with no service. If `status` does not connect within a minute, the cluster cannot reach iroh's relays or the laptop cannot: that is the network of the README's "Getting started", and not this stage's.

- [ ] **Step 5: A pod without the profile does not mount**

The pod's file with draal's ID and the image, and once without `appArmorProfile`:

```bash
sed -e "s|great-machine:v0.2.0|great-machine:nix|" \
    -e "s|1111111111111111111111111111111111111111111111111111111111111111|$GREAT_MACHINE_DRAAL|" \
    deploy/pod.yaml > /tmp/gm4/pod.yaml
sed -e '/appArmorProfile:/,/type: Unconfined/d' -e 's/name: web-example/name: web-confined/' /tmp/gm4/pod.yaml > /tmp/gm4/pod-confined.yaml
kubectl apply -f /tmp/gm4/pod-confined.yaml
sleep 20; kubectl get pod web-confined; kubectl logs web-confined | tail -5
kubectl delete pod web-confined
```

Expected: the container ends at once, again and again (`CrashLoopBackOff` or `Error`), and its log has the sentence of Task 10 that says that the container of a zathras has to let it mount: `CAP_SYS_ADMIN` and, where there is AppArmor, the profile `Unconfined`. Copy the line into the ledger. **If it mounts and runs instead**, the runtime's profile on this node does not deny the mount: say so; the profile then is not needed here, and the README's sentence about it is to be weakened to "where the runtime's profile denies it".

- [ ] **Step 6: The pod as `deploy/pod.yaml` has it**

```bash
kubectl apply -f /tmp/gm4/pod.yaml
sleep 20; kubectl get pod web-example; kubectl logs web-example | tail -8
kubectl exec web-example -- sh -c 'grep " /nix/store " /proc/mounts; ls -A /nix/store | wc -l'
docker exec gm4-control-plane sh -c 'findmnt -t nfs4 | wc -l'
great-machine status web
```

Expected: the pod is `Running` and not ready (`0/1`); its log says that the store is mounted; `/proc/mounts` in the container has `/nix/store` as `nfs4` from `127.0.0.1`; the store is empty; **the node has no NFS mount** (`0`): the mount is the container's own. `status web` lists `web-example` as `idle`.

- [ ] **Step 7: A rollout, from the laptop**

A program for the pods, built by Nix, that goes on running:

```bash
BIN=$(nix build --no-link --print-out-paths nixpkgs#darkhttpd)/bin/darkhttpd
time great-machine rollout web "$BIN" -- /tmp --port 8080
kubectl get pod web-example
great-machine status web
great-machine logs web | tail -5
```

Expected: the steps of the rollout, the push among them with the number of store paths and their size, then `completed`; the pod is ready (`1/1`); the log has darkhttpd's first lines. Note the time of the push. If the push fails at an upload, the URL in the error names the address the laptop could not reach: it has to be `http://$NODE:30900`.

- [ ] **Step 8: What the service and a shell may do**

```bash
echo 'id; grep -E "^(Cap|NoNewPrivs)" /proc/self/status; grep " /nix/store " /proc/mounts | cut -c1-60; mount -t tmpfs none /mnt 2>&1 | tail -1; cat /proc/1/status | grep CapEff' | great-machine shell web
```

Expected: `uid=0`; `CapBnd` and `CapEff` without the bit of `CAP_SYS_ADMIN` (`0x200000`: with a container's default set and `SYS_ADMIN` added, zathras has `00000000a82425fb` and the shell `00000000a80425fb`); `NoNewPrivs: 1`; the `mount` is refused (`Operation not permitted` or `permission denied`); process 1, the zathras that waits for children, still has the bit. Copy the lines into the ledger.

With another user:

```bash
sed -e 's/name: web-example/name: web-user/' \
    -e 's|        # - name: GREAT_MACHINE_USER|        - name: GREAT_MACHINE_USER|' \
    -e 's|        #   value: "1000:1000"|          value: "1000:1000"|' /tmp/gm4/pod.yaml > /tmp/gm4/pod-user.yaml
kubectl apply -f /tmp/gm4/pod-user.yaml
sleep 45; kubectl get pod web-user
great-machine status web
echo 'id; grep -E "^Cap(Eff|Prm)" /proc/self/status' | great-machine shell web web-user
```

Expected: `web-user` becomes ready by itself (draal sends a new pod the stable generation, and its zathras fetches the closure from draal's store); `uid=1000 gid=1000`, and `CapEff` and `CapPrm` all zeros.

- [ ] **Step 9: A second service, and the store's page**

```bash
sed -e 's/name: web-example/name: api-example/' -e 's/value: web$/value: api/' /tmp/gm4/pod.yaml > /tmp/gm4/pod-api.yaml
kubectl apply -f /tmp/gm4/pod-api.yaml
time great-machine rollout api "$BIN" -- /tmp --port 8080
kubectl port-forward deployment/draal 8080:8080 >/dev/null 2>&1 &
sleep 2
curl -s http://localhost:8080/store/api/stats | head -c 600; echo
curl -s http://localhost:8080/store/api/refs | head -c 600; echo
kill %1
```

Expected: the second rollout pushes the same store paths under `/services/api/nix/store/` in a fraction of the first one's time and uploads nothing (the step of the push says so); the statistics count twice as many references as packs; the references begin with `/services/api/` and `/services/web/`. Open `http://localhost:8080/store/` in a browser once, while the forward runs, and see that the page draws.

- [ ] **Step 10: A zathras that is killed, and a pod that goes**

```bash
kubectl exec web-example -- sh -c 'kill -9 $(pidof great-machine | tr " " "\n" | sort -n | sed -n 2p)'
sleep 30; kubectl get pod web-example; kubectl logs web-example | grep -i "serving\|mounted\|pinned" | tail -3
great-machine status web
docker exec gm4-control-plane sh -c 'findmnt -t nfs4 | wc -l'
kubectl delete pod web-example web-user api-example --wait=true
sleep 5
docker exec gm4-control-plane sh -c 'findmnt -t nfs4 | wc -l; ls /var/lib/kubelet/pods | wc -l; dmesg | grep -i nfs | tail -5'
kubectl get pods
```

The `kill -9` is for the zathras that does the work, the second process of the program; process 1 then ends with its status and the kubelet starts the container again.

Expected: the container was restarted once (`RESTARTS 1`) and is ready again within the half minute, having found its cache (`pinned` is not 0 in the line the store logs at its start) and been sent the stable generation; after the pods are deleted the node has no NFS mount, the directories of the three pods are gone from `/var/lib/kubelet/pods`, and the kernel's log has nothing about an NFS server that does not answer. **A pod that stays `Terminating`**, or a directory that stays, is a finding: copy `kubectl describe pod` and the kubelet's last lines (`docker exec gm4-control-plane journalctl -u kubelet | tail -30`).

- [ ] **Step 11: draal away**

```bash
kubectl apply -f /tmp/gm4/pod.yaml; sleep 45; kubectl get pod web-example
kubectl scale deployment/draal --replicas=0; sleep 20
kubectl get pod web-example
kubectl exec web-example -- sh -c 'ls /nix/store | wc -l; wget -q -O- http://127.0.0.1:8080/ | head -c 80'
kubectl scale deployment/draal --replicas=1; kubectl rollout status deployment/draal --timeout=180s; sleep 60
great-machine status web
```

Expected: without draal the pod stays ready, its store lists what it fetched and the service answers; with draal back, `status` shows the zathras `running` on the same generation.

- [ ] **Step 12: Take it away, and write it down**

```bash
kind delete cluster --name gm4
docker image rm ghcr.io/fables-for-robots/great-machine:nix
rm -rf /tmp/gm4
```

In the ledger, under "Task 14": the machine (kernel, Docker, kind and Kubernetes versions), the answer to each of the four questions at the top of this task, the lines the steps asked to copy, the two times of the push, and whatever was not as expected. **What this task cannot show** stays said in the plan and the README: a node with SELinux, where the way is `privileged: true` with `GREAT_MACHINE_USER`, and Kubernetes before 1.30 with the annotation.

- [ ] **Step 13: Commit**

```bash
git add docs/superpowers/plans/2026-10-11-great-machine-stage-4/ledger.md
git commit -m "Docs: stage 4 on a cluster: what a pod may do, and what it leaves on its node"
```
