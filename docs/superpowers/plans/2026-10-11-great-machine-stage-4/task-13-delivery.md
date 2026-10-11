### Task 13: Delivery

What a person meets first: the files they apply, the page they read, and what CI holds the next change to. No Go changes in this task.

- **`deploy/pod.yaml`** is one container: no sidecar, no `store` volume, no startup probe; `CAP_SYS_ADMIN` and no AppArmor profile; a volume for the cache. **`deploy/draal.yaml`** has the bucket, its region and its credentials from a Secret, and a volume for the scratch space of the store.
- **The image** has an empty `/nix/store`, for the store to be mounted on, and `/var/cache/great-machine`.
- **CI** runs `scripts/test-linux.sh`. On GitHub's runners, which have AppArmor, that step is the first run of the mount in a container that has what `deploy/pod.yaml` gives one. If it fails there with `permission denied` where it passes on a laptop, the spec's §5.1 is wrong about what suffices: stop and say so.
- **The README** and **the design** say what is true now. The spec's §15 lists what changes in the design.
- **The module requires a release of jaccard-nfs-nix-store.** If Task 10 was done against a commit of its branch, this is where the release has to exist (Task 1, its last step): `go.mod` names `v0.3.0`, and has no `replace`.

The files of this task name the release `v0.2.0` for the image, which does not exist until the stage is merged and tagged: the files say so themselves, as they did of `v0.1.0`.

**Files:**
- Modify: `.github/workflows/test.yml`, `deploy/draal.yaml`, `deploy/pod.yaml`, `flake.nix`, `README.md`, `docs/superpowers/specs/2026-10-09-great-machine-design.md`

**Interfaces:**
- Consumes: everything. The names of the flags and variables (Tasks 6 and 11), `scripts/test-linux.sh` (Task 8), what draal and a zathras log and refuse.
- Produces: nothing that code uses.

- [ ] **Step 1: The pod is one container, and draal has a bucket; the image has a /nix/store to mount on; CI runs the tests that need root**

deploy/pod.yaml is written anew: one container, with what it takes to mount and no more, and a volume for the cache. deploy/draal.yaml gets the bucket, its region, the credentials from a Secret, and a volume for the scratch space of the store. The image gets the two directories a zathras needs to be there. test.yml runs scripts/test-linux.sh after the script of process 1, and asks the image for its empty /nix/store.

Change `.github/workflows/test.yml`:

```diff
--- a/.github/workflows/test.yml
+++ b/.github/workflows/test.yml
@@ -54,6 +54,14 @@ jobs:
         run: go test -count=1 ./web/... ./cli/ ./e2e/
       - name: pid1
         run: scripts/test-pid1.sh
+      # The tests that need root and the kernel: the store of a zathras
+      # mounted with the kernel's NFS client, and what the launcher drops.
+      # They run in a container that is not privileged and has
+      # CAP_SYS_ADMIN and no AppArmor profile, which is what a pod's
+      # container has (deploy/pod.yaml): that this is enough to mount is
+      # part of what the step shows.
+      - name: linux, as root
+        run: scripts/test-linux.sh
       # Steps that test more are added above this line. The last steps
       # build the program as a release does: Nix builds the page by
       # itself and puts it into the program, whatever the steps above
@@ -67,12 +75,15 @@ jobs:
           # The program that Nix built has the page: its draal is asked for it.
           scripts/test-page.sh ./result/bin/great-machine
       # The image as a release builds it, and what a pod asks of it: that
-      # it runs with another store mounted over /nix/store.
+      # it has a /nix/store to mount the store on, and runs with a store
+      # mounted there.
       - name: image
         run: |
           nix build .#image --option allow-unsafe-native-code-during-evaluation true
           docker load < result
           docker run --rm --tmpfs /nix/store ghcr.io/fables-for-robots/great-machine:nix version
+          docker run --rm --entrypoint sh ghcr.io/fables-for-robots/great-machine:nix \
+            -c 'test -d /nix/store && test -z "$(ls -A /nix/store)" && test -d /var/cache/great-machine'
           docker run --rm --tmpfs /nix/store --entrypoint sh ghcr.io/fables-for-robots/great-machine:nix \
             -c 'test -d /tmp && test -s /etc/ssl/certs/ca-certificates.crt'
           # The draal of the image has the page.
```

Change `deploy/draal.yaml`:

```diff
--- a/deploy/draal.yaml
+++ b/deploy/draal.yaml
@@ -1,9 +1,14 @@
-# draal, the controller: one of it, with a volume for its key and its
-# database.
+# draal, the controller, which is also the store of the fleet: one of it,
+# with a volume for its key and its databases, and an S3 bucket for the
+# packs of the store.
 #
 # What to change: the endpoint IDs of the operators (`great-machine id`
-# prints one), the jaccard-store server's endpoint ID and the prefix,
-# which the sidecars of the pods are given too.
+# prints one) and the bucket. The credentials of the bucket come from a
+# Secret that is made first:
+#
+#   kubectl create secret generic draal-s3 \
+#     --from-literal=AWS_ACCESS_KEY_ID=... \
+#     --from-literal=AWS_SECRET_ACCESS_KEY=...
 #
 # The endpoint ID of draal is what the pods and the operators need. draal
 # logs it when it starts:
@@ -11,9 +16,13 @@
 #   kubectl logs deployment/draal | grep -i 'endpoint'
 #
 # It is that of the key in the volume, and stays as long as the volume.
+# It is the endpoint ID of the store as well: a rollout pushes to it, and
+# the pods fetch from it.
 #
 # draal needs no Service: zathrases and operators reach it over iroh by
-# its endpoint ID.
+# its endpoint ID. The packs themselves travel between them and the
+# bucket, by URLs that draal signs: the laptop and the pods have to reach
+# the bucket under the name draal has for it.
 #
 # The web page of draal listens on the loopback address of the pod, port
 # 8080, where no other pod and no Service reaches it. A forward brings it
@@ -34,7 +43,9 @@ spec:
   accessModes: ["ReadWriteOnce"]
   resources:
     requests:
-      # A key and a database of rollouts: megabytes.
+      # A key, the database of rollouts and that of the store: megabytes.
+      # The database of the store is the only record of what the bucket
+      # holds: back the volume up.
       storage: 1Gi
 ---
 apiVersion: apps/v1
@@ -46,7 +57,9 @@ spec:
   replicas: 1
   # The old draal is gone before the new one starts: two would open the
   # same database, and evict each other at the relay, having one key.
-  # While there is none, every service goes on running.
+  # While there is none, every service goes on running, and a pod serves
+  # what it has fetched; what it has not, it can fetch when draal is
+  # back.
   strategy:
     type: Recreate
   selector:
@@ -61,26 +74,48 @@ spec:
         - name: draal
           # The same tag as the pods' app containers: draal and every
           # zathras are kept on one tag, because a draal and a zathras of
-          # different versions may not speak the same protocol. v0.1.0 is
-          # the first release, which exists once it has been tagged.
-          image: ghcr.io/fables-for-robots/great-machine:v0.1.0
+          # different versions may not speak the same protocol. v0.2.0 is
+          # the first release that keeps the store in draal, which exists
+          # once it has been tagged.
+          image: ghcr.io/fables-for-robots/great-machine:v0.2.0
           # The image's entrypoint is great-machine.
           args: ["draal"]
           env:
-            # Holds draal.key, created on first run, and draal.sqlite.
+            # Holds draal.key, created on first run, draal.sqlite and the
+            # database of the store, store.sqlite.
             - name: GREAT_MACHINE_DATA
               value: /data
             # Who may roll out, read logs and open shells: endpoint IDs
             # with commas between them. Without any, nobody can.
             - name: GREAT_MACHINE_OPERATORS
               value: "2222222222222222222222222222222222222222222222222222222222222222"
-            # Where `great-machine rollout` pushes closures to.
-            - name: JACCARD_SERVER
-              value: "0000000000000000000000000000000000000000000000000000000000000000"
-            # Put before the name of a store path as it is, to make its
-            # reference. The same value the sidecars have.
-            - name: JACCARD_PREFIX
-              value: /great-machine/nix/store/
+            # The bucket the packs of the store are kept in. The service
+            # that has it must honor conditional writes (If-None-Match on
+            # PUT), as AWS S3 does.
+            - name: GREAT_MACHINE_S3_BUCKET
+              value: my-packs
+            # The region of the bucket, which the URLs of the store are
+            # signed for. draal ends at its start without one, unless the
+            # Secret below has AWS_REGION.
+            - name: GREAT_MACHINE_S3_REGION
+              value: eu-central-1
+            # For an S3 service other than AWS: its URL, which the laptop
+            # and the pods are handed too and so have to reach under this
+            # name, and whether the bucket is named in the path of a URL
+            # and not in its host name. Such a service takes any region,
+            # or says which it wants.
+            # - name: GREAT_MACHINE_S3_ENDPOINT
+            #   value: https://s3.example.com
+            # - name: GREAT_MACHINE_S3_PATH_STYLE
+            #   value: "true"
+            # Where a pack is unpacked to be verified before the store
+            # takes it: a volume of its own, as it needs room for two
+            # packs of 16 GiB at the most (--store-verify-jobs times
+            # --store-max-pack-size), which is not the volume of the
+            # databases. The store makes a directory of its own in it,
+            # great-machine-scratch, and empties that one at every start.
+            - name: GREAT_MACHINE_STORE_SCRATCH
+              value: /scratch
             # Where the web page listens. This is the default, written
             # out: loopback, reached with `kubectl port-forward` (see the
             # top of this file). An empty value is no web page. Any
@@ -88,10 +123,21 @@ spec:
             # shell in every container, to whatever can reach the pod.
             - name: GREAT_MACHINE_WEB_ADDR
               value: "127.0.0.1:8080"
+          # AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY, and whatever else
+          # of the AWS SDK's settings the Secret holds.
+          envFrom:
+            - secretRef:
+                name: draal-s3
           volumeMounts:
             - name: data
               mountPath: /data
+            - name: scratch
+              mountPath: /scratch
       volumes:
         - name: data
           persistentVolumeClaim:
             claimName: draal-data
+        # Of the node's own disk, and gone with the pod: nothing in it is
+        # kept from one run of draal to the next.
+        - name: scratch
+          emptyDir: {}
```

Change `deploy/pod.yaml`:

```diff
--- a/deploy/pod.yaml
+++ b/deploy/pod.yaml
@@ -1,9 +1,9 @@
-# A pod of the service `web`: its app container runs zathras, which runs
-# whatever draal rolls out, out of a Nix store the pod does not hold. The
-# sidecar fetches every store path from a jaccard-store server.
+# A pod of the service `web`: one container, which runs zathras. zathras
+# mounts a Nix store of its own on /nix/store, fetches into it whatever
+# draal rolls out, from the store draal keeps, and runs it. The pod holds
+# no image of the service, and has no sidecar.
 #
-# What to change: the server's endpoint ID and the prefix, which are those
-# draal was given; draal's endpoint ID; and the name of the service. In a
+# What to change: draal's endpoint ID, and the name of the service. In a
 # Deployment or a StatefulSet the `spec` below is the template's, and
 # every pod is a zathras under its own name.
 apiVersion: v1
@@ -15,66 +15,57 @@ spec:
   # (--stop-timeout) before it kills it. The kubelet has to wait longer
   # than that, or it kills zathras first.
   terminationGracePeriodSeconds: 150
-  initContainers:
-    # An init container that restarts is a sidecar: it is started before
-    # the app containers and stopped after them.
-    - name: nix-store
-      # The tag its own repository documents. Pin it to a release of that
-      # project in production: its versions are that project's to choose.
-      image: ghcr.io/amber-store/jaccard-nfs-nix-store:latest
-      restartPolicy: Always
-      securityContext:
-        # It mounts, and a mount that is to reach the other containers
-        # (Bidirectional) is allowed to privileged containers alone.
-        privileged: true
-      env:
-        - name: JACCARD_SERVER
-          value: "0000000000000000000000000000000000000000000000000000000000000000"
-        # Put before a name as it is. `great-machine rollout` pushes a
-        # store path under the prefix draal has, so the two are the same.
-        - name: JACCARD_PREFIX
-          value: /great-machine/nix/store/
-        - name: JACCARD_NFS_CACHE
-          value: /cache
-        - name: JACCARD_NFS_MOUNT
-          value: /export
-      volumeMounts:
-        - name: store
-          mountPath: /export
-          mountPropagation: Bidirectional
-        - name: cache
-          mountPath: /cache
-      # The app container starts when the store is mounted, not before.
-      # The directory is a mount point from the start, the volume's, so
-      # what is looked for is the NFS mount on it.
-      startupProbe:
-        exec:
-          command: ["grep", "-q", " /export nfs4 ", "/proc/mounts"]
-        periodSeconds: 1
-        failureThreshold: 60
   containers:
     - name: app
       # The image has zathras, a shell and its tools, and nothing of the
       # service: that comes from the store.
       # The same tag as draal: draal and every zathras are kept on one tag,
       # because a draal and a zathras of different versions may not speak
-      # the same protocol. v0.1.0 is the first release, which exists once
-      # it has been tagged.
-      image: ghcr.io/fables-for-robots/great-machine:v0.1.0
+      # the same protocol. v0.2.0 is the first release without a sidecar,
+      # which exists once it has been tagged.
+      image: ghcr.io/fables-for-robots/great-machine:v0.2.0
       command: ["great-machine", "zathras"]
+      securityContext:
+        # zathras mounts the store. That takes CAP_SYS_ADMIN, and the
+        # runtime's AppArmor profile denies it to a container, so the
+        # container has no profile. It is not privileged: it has the
+        # /proc, the /sys and the devices of any container. The service
+        # and the shells are started without the capability, and without
+        # a way to gain it.
+        #
+        # appArmorProfile is a field of Kubernetes 1.30. Before it, this
+        # annotation on the pod says the same:
+        #   container.apparmor.security.beta.kubernetes.io/app: unconfined
+        capabilities:
+          add: ["SYS_ADMIN"]
+        appArmorProfile:
+          type: Unconfined
+        # Where that is not enough, as on nodes with SELinux, which lets
+        # no container that is not privileged mount NFS, the container is
+        # privileged instead of the two above:
+        #   privileged: true
+        # and the service is given a user that is not root, with
+        # GREAT_MACHINE_USER below: in a privileged container root can
+        # reach the node without any capability.
       env:
         - name: GREAT_MACHINE_DRAAL
           value: "1111111111111111111111111111111111111111111111111111111111111111"
         - name: GREAT_MACHINE_SERVICE
           value: web
         # The name the operator sees is the host name, which is the pod's
-        # name. zathras reaches the sidecar's preload endpoint at
-        # 127.0.0.1:9889, which the containers of a pod share.
+        # name.
+        #
+        # The user and the group the service and the shells run as. Not
+        # set: those of zathras, which is root.
+        # - name: GREAT_MACHINE_USER
+        #   value: "1000:1000"
       volumeMounts:
-        - name: store
-          mountPath: /nix/store
-          # It receives the sidecar's mount and can make none of its own.
-          mountPropagation: HostToContainer
+        - name: cache
+          mountPath: /var/cache/great-machine
+      # What the container needs of memory and CPU is what the service
+      # needs and what the store needs to fetch and to serve: the two
+      # count together.
+      #
       # Ready while the service runs, and at no other time: not before
       # the first rollout, not while the service waits to be started
       # again. Not a liveness probe: that would have the kubelet restart
@@ -87,10 +78,8 @@ spec:
         periodSeconds: 2
         failureThreshold: 1
   volumes:
-    # What the sidecar mounts its server on, and the app sees as the store.
-    - name: store
-      emptyDir: {}
-    # What was fetched. It lives as long as the pod, across restarts of
-    # the sidecar.
+    # What zathras has fetched: every store path once as it came and once
+    # as files. It lives as long as the pod, across restarts of the
+    # container, and nothing is ever removed from it.
     - name: cache
       emptyDir: {}
```

Change `flake.nix`:

```diff
--- a/flake.nix
+++ b/flake.nix
@@ -108,7 +108,8 @@
 
           # The one program: draal, zathras and the operator's commands.
           # Without cgo it is linked statically on Linux, and runs in an
-          # image that has no libc and whose /nix/store is another one.
+          # image that has no libc and whose /nix/store is the one zathras
+          # mounts.
           great-machine = goEnv.buildGoApplication {
             pname = "great-machine";
             inherit src;
@@ -128,8 +129,8 @@
           # The image of the app container of a pod, and of draal:
           #   nix build .#image && docker load < result
           #
-          # Nothing it needs lives in /nix/store, because a pod mounts the
-          # sidecar's store there: the program, busybox and the
+          # Nothing it needs lives in /nix/store, because zathras mounts
+          # the store of its service there: the program, busybox and the
           # certificates are copied to where an image has them. That
           # works because all three are files that need no other.
           image = pkgs.dockerTools.buildLayeredImage {
@@ -139,6 +140,10 @@
               mkdir -p bin etc/ssl/certs root
               mkdir -m 1777 tmp
 
+              # What zathras mounts the store on, and where it keeps
+              # what it fetched unless a volume is put there.
+              mkdir -p nix/store var/cache/great-machine
+
               cp ${great-machine}/bin/great-machine bin/great-machine
 
               # busybox is /bin/sh and the tools of a shell session.
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/test.yml deploy/draal.yaml deploy/pod.yaml flake.nix
git commit -m 'Deploy: a pod of one container and a draal with a bucket; the image has a /nix/store to mount on; CI runs the tests that need root and the kernel'
```

- [ ] **Step 3: The README**

What changes: the head and its picture; what has to be there, and the three steps of getting started that name the store; draal's flags and a section of its own for the store; the flags of zathras, the mount and what the service may do; --jobs; the page of the store; the push of a rollout; the pod; security; what to know; upgrading from v0.1.0; development, with scripts/test-linux.sh.

Change `README.md`:

````diff
--- a/README.md
+++ b/README.md
@@ -3,22 +3,26 @@
 great-machine rolls a program that was built with Nix on a laptop out to a
 group of pods, and shows what those pods are doing.
 
-A pod holds no image of the program. It mounts a Nix store that the
-[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store)
-sidecar serves out of a [jaccard-store](https://github.com/amber-store/jaccard-store)
-server, and its app container runs **zathras**, an init that starts the
-program it is told to run. Every zathras reports to **draal**, the
-controller. The operator talks to draal from the command line.
+A pod holds no image of the program. Its one container runs **zathras**,
+an init that mounts a Nix store of its own on `/nix/store`, fetches into
+it what it is told to run, and runs it. Every zathras reports to
+**draal**, the controller, which also keeps the store of the fleet: every
+store path that was rolled out, as pack files in an S3 bucket. The
+operator talks to draal from the command line.
 
 ```
  laptop                                   cluster
- ┌──────────────────────┐                 ┌───────────────────────────────────┐
- │ great-machine rollout│── push ──────▶  │ jaccard-store server and bucket   │
- │   nix-store -qR      │                 └──────────────▲────────────────────┘
- │                      │                                │ fetch
- │ status, logs, shell  │── iroh ──▶ draal ◀── iroh ── zathras ─ sidecar (/nix/store)
- └──────────────────────┘                                │
-                                                         └─ the service
+ ┌──────────────────────┐                 ┌──────────────────────────────┐
+ │ great-machine rollout│── iroh ───────▶ │ draal: the controller,       │
+ │   nix-store -qR      │                 │        and the store         │
+ │ status, logs, shell  │                 └──────────────▲───────────────┘
+ └──────────┬───────────┘                                │ iroh
+            │ packs up                                   │
+            ▼                                            │
+      ┌───────────┐                       ┌──────────────┴───────────────┐
+      │ S3 bucket │ ◀──── packs down ──── │ pod: zathras, /nix/store     │
+      └───────────┘                       │       └─ the service         │
+                                          └──────────────────────────────┘
 ```
 
 - A **service** is a named group of zathrases that are meant to run the
@@ -33,7 +37,13 @@ controller. The operator talks to draal from the command line.
 Everything travels over [iroh](https://github.com/tmc/go-iroh). draal, an
 operator and every zathras are known by their **endpoint ID**, the public
 half of a key, and the endpoint ID of draal is all that the others need to
-reach it: no address, no port, no Service.
+reach it, for the store as for everything else: no address, no port, no
+Service.
+
+The store is [jaccard-store](https://github.com/amber-store/jaccard-store)
+and the mount is [jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store),
+both inside the program: there is no server of either to run, and a pod
+has no sidecar.
 
 draal also serves [a web page](#the-web-page) that shows the fleet, the
 figures and the log of every zathras, and gives a shell in its container.
@@ -44,9 +54,9 @@ The design is in
 
 ## Getting started
 
-What has to be there: a jaccard-store server and its endpoint ID, a
-cluster whose nodes can run the
-[sidecar](https://github.com/amber-store/jaccard-nfs-nix-store#pod), and
+What has to be there: an S3 bucket and credentials for it, a cluster
+whose nodes have the kernel's NFSv4.1 client, as the common distributions
+do, and let a container with `CAP_SYS_ADMIN` mount (see [Pod](#pod)), and
 Nix on the laptop.
 
 **The network.** draal, every pod and the laptop dial out: to iroh's
@@ -55,14 +65,15 @@ public relays, which carry a connection until a direct path is found
 endpoint is announced and looked up by its ID. A cluster whose egress is
 restricted has to allow that. When it does not, draal warns at its start
 that it has no relay connection, and a zathras never appears in
-`great-machine status`.
+`great-machine status`. The laptop and every pod also reach the bucket:
+pack files go straight between them and it, by URLs that draal signs.
 
 **0. The program.** On the laptop, build it with Nix from the flake.
 The build runs a program while Nix evaluates, which Nix allows only when
 asked (see [Development](#development)):
 
 ```sh
-nix build github:fables-for-robots/great-machine/v0.1.0 \
+nix build github:fables-for-robots/great-machine/v0.2.0 \
   --option allow-unsafe-native-code-during-evaluation true
 ./result/bin/great-machine version
 ```
@@ -82,8 +93,9 @@ prints the endpoint ID of the operator's key, and creates the key the
 first time.
 
 **2. draal.** [`deploy/draal.yaml`](deploy/draal.yaml) runs it in the
-cluster with a volume for its data. Put in the operator's endpoint ID, the
-jaccard-store server's, and a prefix, then:
+cluster with a volume for its data. Put in the operator's endpoint ID and
+the bucket, make the Secret with the bucket's credentials that the file
+names, then:
 
 ```sh
 kubectl apply -f deploy/draal.yaml
@@ -93,8 +105,9 @@ kubectl logs deployment/draal | grep -i endpoint
 draal logs its endpoint ID when it starts. By hand it is:
 
 ```sh
+export AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=...
 great-machine draal --data /var/lib/draal --operator OPERATOR_ID \
-  --jaccard-server SERVER_ID --jaccard-prefix /great-machine/nix/store/
+  --s3-bucket my-packs --s3-region eu-central-1
 ```
 
 Its [web page](#the-web-page) is at `http://127.0.0.1:8080/` on the
@@ -106,16 +119,15 @@ kubectl port-forward deployment/draal 8080:8080    # then http://localhost:8080/
 ```
 
 **3. A pod.** [`deploy/pod.yaml`](deploy/pod.yaml) is a whole pod of the
-service `web`: the sidecar, and an app container that runs
-`great-machine zathras`. Put in the jaccard-store server's endpoint ID and
-the same prefix, and the endpoint ID of draal, then:
+service `web`: one container, which runs `great-machine zathras`. Put in
+the endpoint ID of draal, then:
 
 ```sh
 kubectl apply -f deploy/pod.yaml
 ```
 
-The pod runs and is not ready: its zathras has registered with draal and
-has nothing to run yet.
+The pod runs and is not ready: its zathras has mounted its store, which
+is empty, has registered with draal, and has nothing to run yet.
 
 **4. A rollout.** On the laptop, build the program for the platform of the
 pods and roll it out:
@@ -128,9 +140,9 @@ great-machine rollout web ./result/bin/web -- --listen :8080
 
 The pods run Linux, so the program has to be a Linux one: on a Mac that
 takes a Linux builder for Nix. What follows `--` are the arguments of the
-program. The command pushes the closure, has every zathras of `web` fetch
-it, moves them to it one at a time, and ends when all of them run it. The
-pod is ready now.
+program. The command pushes the closure to draal's store, has every
+zathras of `web` fetch it, moves them to it one at a time, and ends when
+all of them run it. The pod is ready now.
 
 ```sh
 great-machine status web
@@ -165,21 +177,34 @@ there are.
 ### draal
 
 ```sh
-great-machine draal --data DIR --jaccard-server ID --jaccard-prefix P [--operator ID]... [--web-addr ADDR]
+great-machine draal --data DIR --s3-bucket NAME [--s3-region R] [--operator ID]... [--web-addr ADDR]
 ```
 
 | flag | environment | default | meaning |
 | --- | --- | --- | --- |
-| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
+| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run), `draal.sqlite` and the database of the store, `store.sqlite` |
 | `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
-| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
-| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
+| `--s3-bucket NAME` | `GREAT_MACHINE_S3_BUCKET` | required | the bucket of [the store](#the-store) |
+| `--s3-prefix P` | `GREAT_MACHINE_S3_PREFIX` | none | put before the name of every object in the bucket |
+| `--s3-endpoint URL` | `GREAT_MACHINE_S3_ENDPOINT` | AWS | an S3 service other than AWS, as `http://HOST[:PORT]` or `https://HOST[:PORT]`, with the path the service is under if it has one |
+| `--s3-region R` | `GREAT_MACHINE_S3_REGION` | the AWS SDK's, as `AWS_REGION` | the region of the bucket, which the URLs of the store are signed for; one of the two has to say it |
+| `--s3-path-style` | `GREAT_MACHINE_S3_PATH_STYLE` | off | the bucket in the path of a URL, not in its host name |
+| `--store-scratch DIR` | `GREAT_MACHINE_STORE_SCRATCH` | `--data` | where packs are verified: the store makes a directory of its own there, `great-machine-scratch`, and empties that one at every start |
+| `--store-verify-jobs N` | `GREAT_MACHINE_STORE_VERIFY_JOBS` | `2` | packs verified at once |
+| `--store-max-pack-size SIZE` | `GREAT_MACHINE_STORE_MAX_PACK_SIZE` | `16GiB` | the largest pack, uncompressed: bytes, or a number with `KiB`, `MiB` or `GiB` |
+| `--store-no-verify` | `GREAT_MACHINE_STORE_NO_VERIFY` | off | take a pack without reading its data |
 | `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where [the web page](#the-web-page) listens, as `HOST:PORT`; empty for no web page |
 | `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |
 
-At start draal logs its endpoint ID, and the URL of its web page. Without
-`--operator` it warns that nobody can act as an operator. An address for
-the web page that is taken ends the command with 1.
+At start draal logs its endpoint ID, the bucket, and the URL of its web
+page. Without `--operator` it warns that nobody can act as an operator. An
+address for the web page that is taken ends the command with 1. The
+credentials of the bucket come from the AWS SDK's default chain:
+`AWS_ACCESS_KEY_ID` and the rest, a profile, or a role. **draal asks the
+bucket nothing when it starts**: a bucket that is not there, or
+credentials that are wrong, show in the first push, and not before. A
+draal without a region for its bucket does end at its start, and says
+so.
 
 draal keeps the services, their generations and their rollouts in
 `draal.sqlite`. Which zathrases are connected, and what they report, it
@@ -189,6 +214,51 @@ it runs and dials again, and a rollout that was under way is carried on
 with when draal is back. After it starts, draal waits 45 seconds before it
 decides anything, so that the zathrases are back first.
 
+#### The store
+
+draal is the store of the fleet: a
+[jaccard-store](https://github.com/amber-store/jaccard-store) server on
+draal's own endpoint, under draal's endpoint ID. A rollout pushes to it,
+and the pods fetch from it.
+
+- **Every service has references of its own.** A store path that was
+  pushed for the service `web` is the reference
+  `/services/web/nix/store/NAME`, and the `/nix/store` of a pod is made
+  of its service's alone. A store path that two services run is pushed
+  for each; the second time nothing is uploaded, as the store has its
+  pack already.
+- **Packs do not pass through draal.** They go between the laptop, the
+  pods and the bucket by URLs that draal signs, each valid for an hour.
+  So the laptop and the pods have to reach the bucket, and under the
+  name draal has for it: the `--s3-endpoint` that draal is given is the
+  one in the URLs.
+- **The bucket's service must honor conditional writes**
+  (`If-None-Match: *` on PUT, as AWS S3 does): upload URLs are signed
+  with that condition, so that a pack the store has verified cannot be
+  replaced.
+- **A pack is verified before a reference points at it**: draal downloads
+  it, unpacks it into its scratch directory and checks every object
+  against its key. That takes room for `--store-max-pack-size` times
+  `--store-verify-jobs`, 32 GiB with the defaults, which is why
+  `deploy/draal.yaml` gives the scratch space a volume of its own. The
+  scratch directory is `great-machine-scratch` inside `--store-scratch`,
+  made by the store and emptied whenever draal starts; nothing else in
+  `--store-scratch` is touched, so it may be any directory, the data
+  directory among them. A
+  store path whose pack is larger than `--store-max-pack-size` cannot be
+  pushed. `--store-no-verify` leaves the verification out: a pack that is
+  damaged is then found by the pod that fetches it, and not by the
+  rollout that pushed it.
+- **`store.sqlite` is the only record** of the references and of which
+  pack leans on which. The bucket alone does not describe the store: back
+  the data directory up.
+- **Nothing is removed from the store.** A store path that no generation
+  needs any more stays in the bucket.
+- **Who may do what.** Operators push. Anyone who knows the endpoint ID
+  of draal can fetch: see [Security](#security).
+- What the store holds, and what sharing and compression save, is on
+  draal's [web page](#the-web-page) under `/store/`.
+
 ### zathras
 
 ```sh
@@ -200,7 +270,9 @@ great-machine zathras --draal ID --service NAME
 | `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
 | `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
 | `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
-| `--preload-url URL` | `GREAT_MACHINE_PRELOAD_URL` | `http://127.0.0.1:9889/v1/preload` | the preload endpoint of the sidecar |
+| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | what the store of this zathras has fetched |
+| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | mount nothing: `/nix/store` is whatever the container has |
+| `--user UID[:GID]` | `GREAT_MACHINE_USER` | those of zathras | the user and the group the service and the shells run as |
 | `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
 | `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |
 | `--health-addr ADDR` | `GREAT_MACHINE_HEALTH_ADDR` | `:9890` | where the health check listens; empty for none |
@@ -208,6 +280,19 @@ great-machine zathras --draal ID --service NAME
 zathras is the first process of the app container. It runs the service
 with its own environment and working directory, and:
 
+- **It mounts the store of its service on `/nix/store`** when it starts,
+  and ends at once, saying what the container lacks, when it cannot (see
+  [Pod](#pod)). The store is an NFS server inside zathras, on a loopback
+  port, that only zathras mounts; a store path is fetched from draal the
+  first time it is needed, and every path of a rollout before the
+  rollout touches the service. What was fetched is kept in
+  `--store-cache`, twice: as it came, and as plain files. Nothing is
+  removed from it. A cache is of one draal and one service.
+- **The service and the shells run without `CAP_SYS_ADMIN`**, which
+  zathras itself needs to mount, and cannot gain it. With `--user` they
+  run as that user and group, with no capability at all; their standard
+  output and standard error, and the terminal of a shell, are theirs, so
+  a program that opens `/dev/stdout` anew can.
 - **A service that ends is started again**, whatever its exit status: at
   once if it ran for 30 seconds or longer, and otherwise after a delay of
   1 second, then twice as long each time, up to 2 minutes.
@@ -221,7 +306,11 @@ with its own environment and working directory, and:
   service.
 - **A zathras makes a new key every time it starts.** A container that
   was restarted is a new zathras under the old name: it has nothing to
-  run until draal has sent it the generation of its service.
+  run until draal has sent it the generation of its service. What it had
+  fetched is still in its cache, when the cache is a volume of the pod.
+- `--no-store` is for a machine that has a `/nix/store` of its own, and
+  for a system that is not Linux, where zathras cannot mount: nothing is
+  fetched, and a rollout runs what is there.
 
 ### Operator commands
 
@@ -244,13 +333,18 @@ and `tui` take both of these:
 
 | flag | environment | default | meaning |
 | --- | --- | --- | --- |
-| `--jobs N` | `GREAT_MACHINE_JOBS` | `5` | the number of store paths pushed at a time |
+| `--jobs N` | `GREAT_MACHINE_JOBS` | `20` | the number of store paths pushed at a time, 32 at the most: the store answers 64 requests of a connection at a time |
 | `--temp-dir DIR` | `GREAT_MACHINE_TEMP_DIR` | the system's temporary directory | where the temporary stores of a push are made |
 
+Every store path that is being pushed is in `--temp-dir` once more,
+uncompressed, with its pack beside it: up to `--jobs` of them at a time. A
+laptop that is short of room there gives a smaller `--jobs`.
+
 `logs` takes `-f` (`GREAT_MACHINE_FOLLOW`): go on with every new line.
 
 `shell` is a shell in the container of the zathras: it sees the store,
-the files and the processes the service sees. On a terminal, `Ctrl-]`
+the files and the processes the service sees, as the user the service
+runs as. On a terminal, `Ctrl-]`
 leaves the session. With a standard input that is no terminal the input
 is passed on, and the exit status of the shell is that of the command:
 
@@ -271,6 +365,7 @@ says otherwise:
 | services | `/` | every service: its stable generation, its zathrases by phase, the last rollout and how far it is |
 | a service | `/s/SERVICE` | its zathrases (name, phase, generation, time running, restarts, CPU and memory), the rollout under way with every zathras' part in it, and the last 20 rollouts |
 | a zathras | `/s/SERVICE/z/ID` | its state in full; CPU, memory, disk and network over the last 15 minutes; the last 400 lines of its log, followed as they come; a shell in its container |
+| the store | `/store/` | what [the store](#the-store) holds: its references and packs, and what sharing and compression save. It is jaccard-store's own page, and changes nothing |
 
 The page is what `status`, `logs` and `shell` are on the command line.
 **It can roll nothing out and nothing back**: a rollout needs the Nix
@@ -433,11 +528,13 @@ great-machine rollout [--parallel N] [--detach] SERVICE PATH [-- ARG...]
    so `./result/bin/web` works. A binary for macOS is refused.
 2. **The closure** is what `nix-store --query --requisites` prints for the
    store path the binary is in.
-3. **The push.** The command asks draal for the store, closes that
-   connection, and pushes: every store path of the closure that the
-   jaccard-store server does not have yet is pushed to it, `--jobs` at a
-   time, as the reference `prefix + name`. If a path fails, nothing is
-   rolled out.
+3. **The push.** The command asks draal for the store of the service,
+   closes that connection, and pushes to draal's endpoint ID: every store
+   path of the closure that the store does not have yet for the service
+   is pushed, `--jobs` at a time, as the reference
+   `/services/SERVICE/nix/store/NAME`. A store path that another service
+   has pushed is read again on the laptop and not uploaded again. If a
+   path fails, nothing is rolled out.
 4. **The rollout.** The command dials draal again to send the rollout, as
    the operator's key must not have two endpoints at once. If that second
    dial fails, the closure was pushed and nothing was rolled out, and
@@ -459,9 +556,9 @@ A rollout has two phases, and a state:
 | `halted` | it failed, and the reason is recorded |
 | `superseded` | another rollout of the service was begun before this one ended |
 
-- **Preparing.** Every zathras has its sidecar fetch the closure. Nothing
-  is restarted before all of them have it, so a path that cannot be
-  fetched halts the rollout while the service still runs everywhere.
+- **Preparing.** Every zathras fetches the closure into its store.
+  Nothing is restarted before all of them have it, so a path that cannot
+  be fetched halts the rollout while the service still runs everywhere.
 - **Activating.** A zathras stops its service and starts the new binary.
   It counts as done when the new binary has run for 30 seconds, and the
   next one follows. A zathras that runs nothing yet is not waited for.
@@ -524,37 +621,67 @@ readinessProbe:
 
 ## Pod
 
-[`deploy/pod.yaml`](deploy/pod.yaml) is the sidecar as
-[jaccard-nfs-nix-store documents it](https://github.com/amber-store/jaccard-nfs-nix-store#pod)
-and an app container:
+[`deploy/pod.yaml`](deploy/pod.yaml) is a whole pod. It has one container:
 
 ```yaml
 containers:
   - name: app
-    image: ghcr.io/fables-for-robots/great-machine:v0.1.0
+    image: ghcr.io/fables-for-robots/great-machine:v0.2.0
     command: ["great-machine", "zathras"]
+    securityContext:
+      capabilities: { add: ["SYS_ADMIN"] }
+      appArmorProfile: { type: Unconfined }
     env:
       - { name: GREAT_MACHINE_DRAAL, value: <endpoint id of draal> }
       - { name: GREAT_MACHINE_SERVICE, value: web }
     volumeMounts:
-      - { name: store, mountPath: /nix/store, mountPropagation: HostToContainer }
+      - { name: cache, mountPath: /var/cache/great-machine }
     readinessProbe:
       httpGet: { path: /healthz, port: 9890 }
+volumes:
+  - { name: cache, emptyDir: {} }
 terminationGracePeriodSeconds: 150   # longer than --stop-timeout
 ```
 
-- **The prefix of the sidecar is that of draal.** A rollout pushes a store
-  path as `prefix + name`, and the sidecar looks it up as that.
+- **The container can mount, and is not privileged.** zathras mounts the
+  store on `/nix/store`, which takes `CAP_SYS_ADMIN`, and which the
+  runtime's AppArmor profile denies, so the container has none. It keeps
+  what the runtime does for every container: a masked `/proc`, a `/sys`
+  that cannot be written, the devices of a container. `appArmorProfile`
+  is a field of Kubernetes 1.30; before it, the annotation
+  `container.apparmor.security.beta.kubernetes.io/app: unconfined` on the
+  pod says the same. A zathras that cannot mount ends at once and says
+  that this is what it lacks.
+- **The service does not have the capability.** zathras starts the
+  service and the shells without `CAP_SYS_ADMIN` and without a way to
+  gain it, by a setuid program or otherwise: they have what an ordinary
+  container has. With `GREAT_MACHINE_USER` they are not root either.
+- **Where that is not enough**, as on nodes with SELinux, which lets no
+  container that is not privileged mount NFS: `privileged: true` in the
+  place of the two settings, **and** `GREAT_MACHINE_USER`. Root in a
+  privileged container reaches the node without any capability (the
+  node's disks, `/proc/sysrq-trigger`), so there the service should not
+  be root. zathras warns at its start when it is.
+- **The mount is the container's own.** It is made inside the container,
+  on no volume, so it reaches no other container and not the node, and it
+  is gone with the container.
+- **The cache** is a volume of the pod, so that a container that was
+  restarted finds what was fetched. It holds every store path the pod
+  has fetched, twice, and grows until the pod is gone.
+- **The node** needs the kernel's NFSv4.1 client.
 - **The environment of the service is that of the container.** What the
   program needs of settings and secrets is put on the app container.
+- **What the container is given of memory and CPU** is for the service
+  and for the store together: fetching a store path and serving it count
+  with the service, and so do they in the figures of the zathras.
 - **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
   arm64, holds the program, a static busybox for `/bin/sh` and the tools
-  of a shell session, CA certificates and a `/tmp`. Nothing in it needs
-  its own `/nix/store`, which the sidecar's is mounted over. It is the
-  image of draal too. Name it by a release tag, as `v0.1.0`, the first
-  release, which exists once it has been tagged, and keep draal and every
-  zathras on the same tag: a draal and a zathras of different versions may
-  not speak the same protocol.
+  of a shell session, CA certificates, a `/tmp`, and an empty
+  `/nix/store` for the store to be mounted on. Nothing in it needs a
+  `/nix/store` of its own. It is the image of draal too. Name it by a
+  release tag, as `v0.2.0`, which exists once it has been tagged, and
+  keep draal and every zathras on the same tag: a draal and a zathras of
+  different versions may not speak the same protocol.
 
 ## Security
 
@@ -587,11 +714,26 @@ terminationGracePeriodSeconds: 150   # longer than --stop-timeout
   every watcher, and a fleet that comes to more than a message holds
   (4 MiB) ends `status` and the following of a rollout, which draal
   logs.
-- **The jaccard-store server** takes a push from anyone who can reach it,
-  and the sidecar's preload endpoint has no authentication, which is why
-  it listens on the loopback address of the pod.
-- A shell runs as the user of the app container, with everything the
-  service can see.
+- **The store takes a push from operators alone**, and so a listing and
+  a removal. **Fetching is open**, as registration is: whoever knows the
+  endpoint ID of draal can fetch any store path of any service by its
+  name. The prefixes keep the stores of the services apart, and not their
+  contents secret. Put no secret into a store path, which a Nix store is
+  no place for anyway.
+- **The app container has `CAP_SYS_ADMIN`**, for zathras to mount the
+  store, and no AppArmor profile; it is not privileged. The service and
+  the shells are started without the capability and with `no_new_privs`,
+  and cannot trace zathras, which has more than they do. As root they
+  can still do what root in an ordinary container can, in their own
+  container, the cache of the store included. `--user` makes them
+  another user. In a container that has to be privileged, give `--user`:
+  see [Pod](#pod).
+- **The store of a pod listens on loopback**, on a port the system
+  picks, for the kernel of the node to mount: whoever is in the pod's
+  network namespace can read the store, and nothing else can. Nothing in
+  the pod takes requests to fetch.
+- A shell runs as the user of the service, with everything the service
+  can see.
 
 ## What to know
 
@@ -600,7 +742,41 @@ terminationGracePeriodSeconds: 150   # longer than --stop-timeout
 - The first rollout of a service makes the service. Nothing removes one.
 - A zathras that is gone is forgotten; its log goes with its container.
 - The figures of a zathras (CPU, memory, disk, network) are read from
-  cgroup v2. On a node without it there are none.
+  cgroup v2. On a node without it there are none. They are those of the
+  container: the service, zathras and its store, and the shells.
+- A whole store path is fetched for one file of it. A fetch that takes
+  longer than three minutes is an I/O error for the process that waits
+  for it; the fetch goes on, and the next access finds it done. A rollout
+  fetches every path before it starts anything, so this is met by a
+  program that names a path outside its closure.
+- While draal cannot be reached, a pod serves what it has fetched and
+  can fetch nothing new.
+
+## Upgrading from v0.1.0
+
+Up to v0.1.0 the store was a jaccard-store server of its own, and every
+pod had a sidecar. From v0.2.0 on draal is the store and zathras mounts
+it, so:
+
+1. **draal gets a bucket**, with `--s3-bucket`, a region and
+   credentials. `--jaccard-server` and `--jaccard-prefix` are gone: a
+   draal that is still given the flags ends and names them, and the
+   variables `JACCARD_SERVER` and `JACCARD_PREFIX` are read no more. Its
+   data directory stays: the services, their generations and their
+   rollouts are kept.
+2. **Every pod becomes one container**, as in
+   [`deploy/pod.yaml`](deploy/pod.yaml): no sidecar, no `store` volume,
+   the `securityContext`, the `cache` volume. draal and the pods change
+   together, as with any release.
+3. **Every service is rolled out once more**, with the binary it runs if
+   nothing else is to change. draal's store begins empty: what the old
+   server holds is not taken over, and the stable generation of a
+   service names store paths that the new store does not have. Until the
+   service is rolled out again, a pod that starts fetches nothing, says
+   which store path the store lacks, and stays not ready.
+
+The old jaccard-store server and its bucket are not touched by any of
+this, and can go when every service has been rolled out again.
 
 ## Development
 
@@ -617,8 +793,9 @@ nix build --option allow-unsafe-native-code-during-evaluation true
 nix develop --option allow-unsafe-native-code-during-evaluation true
 ```
 
-In the dev shell are `go`, `sqlc`, `node` with `npm`, and `great-machine`
-as built from the tree, with the web page in it. With direnv, `.envrc`
+In the dev shell are `go` (1.27, which the packages of the store's NFS
+server need), `sqlc`, `node` with `npm`, and `great-machine` as built from
+the tree, with the web page in it. With direnv, `.envrc`
 passes the option and reloads the shell, rebuilding the program, when the
 sources of the program or of the page change. Nix sees only the files git
 tracks, so `git add` new ones.
@@ -634,10 +811,24 @@ because building the program is part of it: after a `go get` that left
 `node` without the program and needs no option; run `go mod tidy` there.
 
 The tests need no cluster and no network beyond the loopback interface.
-Those in `e2e` run draal and several zathrases in one process, with real
-child processes for the services, and the terminal UI against them in a
-pseudo-terminal. `scripts/test-pid1.sh` runs zathras as
-the first process of a container, and takes Docker.
+Those in `e2e` run draal, with its store over an S3 in memory, and
+several zathrases in one process, with real child processes for the
+services, and the terminal UI against them in a pseudo-terminal.
+`scripts/test-pid1.sh` runs zathras as the first process of a container,
+and takes Docker.
+
+The store of a zathras is mounted with the kernel's NFS client, and what
+zathras drops for the service is the kernel's too: the tests of both need
+Linux and root, and are skipped anywhere else. The mount itself is built
+for Linux alone; on a Mac `great-machine zathras` runs with `--no-store`.
+`scripts/test-linux.sh` runs the tests as root in a container that has
+what a pod's container has, `CAP_SYS_ADMIN` and no AppArmor profile, and
+is not privileged:
+
+```sh
+scripts/test-linux.sh                              # every package
+scripts/test-linux.sh -run Mount -v ./storemount/  # the arguments are go test's
+```
 
 ### The page
 
@@ -719,7 +910,8 @@ release is built with the tag in `GREAT_MACHINE_VERSION` and `--impure`.
 `go vet`, the tests, `sqlc diff` (the generated queries are what the SQL
 says), the type check and the build of the page and the tests of what
 serves it once more with it, zathras as the first process of a container,
-the Nix build and the image. `scripts/test-page.sh` asks the program that
+the tests that need root and the kernel (`scripts/test-linux.sh`), the
+Nix build and the image. `scripts/test-page.sh` asks the program that
 Nix built, and the draal of the image, for the page.
 
 A tag `v*` is a release: `.github/workflows/release.yml` builds the image
@@ -729,7 +921,7 @@ GitHub release with notes generated from what was merged since the last
 one.
 
 ```sh
-git tag v0.1.0 && git push origin v0.1.0
+git tag v0.2.0 && git push origin v0.2.0
 ```
 
 ## License
````

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m 'README: draal is the store and a pod is one container: getting started, the flags, the store, the pod, security, and upgrading from v0.1.0'
```

- [ ] **Step 5: The design**

The spec of this stage lists in its §15 what the design is brought up to. The sections keep their numbers; 7.8 is new, and points at the spec of stage 4 for what it only names.

Change `docs/superpowers/specs/2026-10-09-great-machine-design.md`:

````diff
--- a/docs/superpowers/specs/2026-10-09-great-machine-design.md
+++ b/docs/superpowers/specs/2026-10-09-great-machine-design.md
@@ -7,23 +7,36 @@
 great-machine rolls a program that was built with Nix on a laptop out to a
 group of pods, and shows what those pods are doing.
 
-A pod holds no image of the program. It mounts a Nix store that the
-[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store)
-sidecar serves out of a [jaccard-store](https://github.com/amber-store/jaccard-store)
-server, and its app container runs **zathras**, an init that starts the
-program it is told to run. Every zathras reports to **draal**, the
-controller. The operator talks to draal: from the command line to roll out,
-and through a web page or a terminal UI to look.
+A pod holds no image of the program. Its one container runs **zathras**,
+an init that mounts a Nix store on `/nix/store`, fetches into it what it
+is told to run, and starts it. Every zathras reports to **draal**, the
+controller, which is also the store they fetch from. The operator talks
+to draal: from the command line to roll out, and through a web page or a
+terminal UI to look.
+
+The store is a [jaccard-store](https://github.com/amber-store/jaccard-store)
+server over an S3 bucket, and the mount is
+[jaccard-nfs-nix-store](https://github.com/amber-store/jaccard-nfs-nix-store);
+both are inside the program. Up to stage 3 the store was a server of its
+own, and the mount a sidecar of every pod. [The design of stage 4](2026-10-11-great-machine-stage-4-design.md)
+is how both came into the program, and has what this document only names
+of them.
 
 ```
  laptop                                   cluster
- ┌──────────────────────┐                 ┌───────────────────────────────────┐
- │ great-machine rollout│── push ──────▶  │ jaccard-store server and bucket   │
- │   nix-store -qR      │                 └──────────────▲────────────────────┘
- │                      │                                │ fetch
- │ great-machine tui    │── iroh ──▶ draal ◀── iroh ── zathras ─ sidecar (/nix/store)
- └──────────────────────┘             │                  │
-        browser ── HTTP, loopback ────┘                  └─ the service
+ ┌──────────────────────┐                 ┌──────────────────────────────┐
+ │ great-machine rollout│── iroh ───────▶ │ draal, one endpoint ID:      │
+ │   nix-store -qR      │  both protocols │   great-machine/1            │
+ │ great-machine tui    │                 │   amber/jaccard-store/1      │
+ └──────────┬───────────┘                 └───▲──────────▲───────────────┘
+            │                 browser ── HTTP ┘          │ iroh: great-machine/1,
+            │ packs up              (loopback)           │ and pulls of the mount
+            ▼                                            │
+      ┌───────────┐                       ┌──────────────┴───────────────┐
+      │ S3 bucket │ ◀──── packs down ──── │ pod, one container: zathras  │
+      └───────────┘                       │   NFS on 127.0.0.1 ─▶ /nix/store
+                                          │   └─ the service             │
+                                          └──────────────────────────────┘
 ```
 
 It is done when:
@@ -84,10 +97,9 @@ Every flag has an environment variable, and a flag wins over its variable.
 
 | Flag | Variable | Default | Meaning |
 |---|---|---|---|
-| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run) and `draal.sqlite` |
+| `--data DIR` | `GREAT_MACHINE_DATA` | required | holds `draal.key` (created on first run), `draal.sqlite` and the store's `store.sqlite` |
 | `--operator ID` | `GREAT_MACHINE_OPERATORS` | none | an endpoint ID that may act as an operator; may be repeated, and the variable takes several with commas between them |
-| `--jaccard-server ID` | `JACCARD_SERVER` | required | the jaccard-store server that closures are pushed to |
-| `--jaccard-prefix P` | `JACCARD_PREFIX` | required | what is put before the name of a store path to make its reference; the same value the sidecars are given |
+| `--s3-bucket NAME` | `GREAT_MACHINE_S3_BUCKET` | required | the bucket of the store; with it `--s3-prefix`, `--s3-endpoint`, `--s3-region`, `--s3-path-style`, and `--store-scratch`, `--store-verify-jobs`, `--store-max-pack-size`, `--store-no-verify` ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.6) |
 | `--web-addr ADDR` | `GREAT_MACHINE_WEB_ADDR` | `127.0.0.1:8080` | where the web page listens; empty for no web page |
 | `--bind IP:PORT` | `GREAT_MACHINE_BIND` | every address, a port the system picks | the UDP address of the iroh endpoint |
 
@@ -102,7 +114,9 @@ act as an operator over iroh.
 | `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
 | `--service NAME` | `GREAT_MACHINE_SERVICE` | required | the service this zathras belongs to |
 | `--name NAME` | `GREAT_MACHINE_NAME` | the host name, which in a pod is the pod's name | what the operator sees |
-| `--preload-url URL` | `GREAT_MACHINE_PRELOAD_URL` | `http://127.0.0.1:9889/v1/preload` | the preload endpoint of the sidecar |
+| `--store-cache DIR` | `GREAT_MACHINE_STORE_CACHE` | `/var/cache/great-machine` | what the mount has fetched |
+| `--no-store` | `GREAT_MACHINE_NO_STORE` | off | mount nothing: `/nix/store` is whatever the container has |
+| `--user UID[:GID]` | `GREAT_MACHINE_USER` | zathras's own | the user and the group the service and the shells run as |
 | `--shell PATH` | `GREAT_MACHINE_SHELL` | `/bin/sh` | the program of a shell session |
 | `--stop-timeout D` | `GREAT_MACHINE_STOP_TIMEOUT` | `2m` | how long a service has to end after SIGTERM |
 | `--health-addr ADDR` | `GREAT_MACHINE_HEALTH_ADDR` | `:9890` | where the health check listens; empty for none |
@@ -114,24 +128,29 @@ act as an operator over iroh.
 | `--draal ID` | `GREAT_MACHINE_DRAAL` | required | the endpoint ID of draal |
 | `--key FILE` | `GREAT_MACHINE_KEY` | `great-machine/operator.key` in the user's configuration directory | the operator's key, created on first use |
 
-`rollout` also takes `--jobs N` (`GREAT_MACHINE_JOBS`, default 5), the
+`rollout` also takes `--jobs N` (`GREAT_MACHINE_JOBS`, default 20), the
 number of store paths pushed at a time, and `--temp-dir DIR`
 (`GREAT_MACHINE_TEMP_DIR`), where the temporary stores of a push are made.
 
 ## 4. Identity and authorization
 
-Everything but the web page travels over iroh
-([go-iroh](https://github.com/tmc/go-iroh) v0.3.0), on one ALPN,
-`great-machine/1`. A peer is known by its endpoint ID, which is the public
-half of its key and which the handshake proves.
+Everything but the web page, and the packs of the store, travels over
+iroh ([go-iroh](https://github.com/tmc/go-iroh) v0.3.0). The endpoint of
+draal accepts two ALPNs: `great-machine/1`, which is the protocol of
+section 5, and `amber/jaccard-store/1`, jaccard-store's own, for the
+store. A peer is known by its endpoint ID, which is the public half of
+its key and which the handshake proves.
 
-- **draal** has a key in its data directory. Its endpoint ID is its address.
+- **draal** has a key in its data directory. Its endpoint ID is its
+  address, and that of the store.
 - **An operator** has a key file. `great-machine id` prints its endpoint ID,
   which draal is given with `--operator`.
 - **A zathras** makes a new key every time it starts and keeps it in
   memory. Its endpoint ID is its identity for as long as the process lives,
   across reconnections. Pods cannot share a key: two endpoints with one key
   evict each other at the relay, and draal could not tell them apart.
+  The mount of a zathras pulls from the store with a second key, made
+  the same way, for connections that come and go at other times.
 
 Every request passes one function of draal before anything is done:
 
@@ -143,7 +162,8 @@ Authorize(peer EndpointID, op Operation, service string) error
 | Operation | Allowed to |
 |---|---|
 | register as a zathras of a service | every peer |
-| everything else | the endpoint IDs given with `--operator` |
+| pull a reference from the store | every peer |
+| everything else, the store's push, list and delete among it | the endpoint IDs given with `--operator` |
 
 Endpoint IDs are compared exactly, and an empty one among the operators
 is ignored.
@@ -180,6 +200,11 @@ trusted to be small, well-formed or harmless to show:
   than one of its fields set is refused.
 - An answer that cannot be written, for another reason than the peer
   having gone, is logged by draal with the peer and the request.
+- **The store's protocol** has the same bounds for its streams: a request
+  within 5 seconds, 64 streams of a connection at a time. What a request
+  holds is jaccard-store's server's to check. A refusal there is an error
+  of that protocol whose message begins with `unauthorized`
+  ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.3 and §3.4).
 
 What is not bounded is the number of peers that register. The fleet is
 sent whole, and enough registrants that each report as much as they may
@@ -247,7 +272,7 @@ The operator dials draal and opens:
 
 | Request | Answer |
 |---|---|
-| `store` | the jaccard-store server and the prefix |
+| `store`: service | the endpoint ID of the store, which is draal's own, and the prefix of the service's references, `/services/SERVICE/nix/store/` |
 | `rollout`: service, binary, arguments, closure, parallel | the rollout (its number, the generation) |
 | `rollback`: service, parallel | the rollout |
 | `watch` | the fleet (5.3): at once, and again whenever something changed, at most once a second |
@@ -297,15 +322,20 @@ ID, name, the time it connected and its state.
    there. A Mach-O binary is refused: the pods run Linux.
 2. **The closure.** `nix-store --query --requisites` of the store path the
    binary is in.
-3. **The store.** draal is asked for the jaccard-store server and the
-   prefix, and the connection to draal is closed. The push dials with
-   the operator's key as well, and two endpoints with one key put each
-   other out at the relay: the key has one endpoint at a time.
-4. **The push.** The server is asked which of the references
+3. **The store.** draal is asked for the store of the service, which is
+   draal's own endpoint ID and the prefix of the service, and the
+   connection to draal is closed. The push dials draal again, for the
+   store's protocol and with the operator's key as well, and two
+   endpoints with one key put each other out at the relay: the key has
+   one endpoint at a time.
+4. **The push.** The store is asked which of the references
    `prefix + name` it has. A store path never changes under its name, so
    one that is there is not pushed again. Every other path is imported into
    a temporary store, as `jaccard-store push-dir` imports a directory, and
-   pushed with the `client` package of jaccard-store, `--jobs` at a time. A
+   pushed with the `client` package of jaccard-store, `--jobs` at a time,
+   20 unless told otherwise. One that another service has pushed is
+   imported and not uploaded: the store has a pack of that content and
+   points the new reference at it. A
    store path that is a single file is pushed as a reference whose root is
    the file. A store path that is itself a symbolic link cannot be a
    reference (a link exists only as an entry of a directory), so a closure
@@ -364,13 +394,13 @@ anything, so that the zathrases are back before it decides: a zathras
 dials again after 30 seconds at most.
 
 **Preparing.** Every connected zathras that has not prepared the target is
-sent `prepare`. The zathras posts the closure to the preload endpoint of
-the sidecar and then checks that the binary is a file it may execute.
+sent `prepare`. The zathras fetches the closure into its store (7.3) and
+then checks that the binary is a file it may execute.
 
 - When every connected zathras has prepared the target, the rollout is
   `activating`. With no zathras connected that is at once.
 - A `prepare` that fails halts the rollout, with the name of the zathras
-  and what the sidecar said.
+  and which store path it could not fetch, and why.
 - A rollout that is still `preparing` 30 minutes after it was made is
   halted, and the reason names the zathrases that had not prepared.
 
@@ -438,13 +468,17 @@ the generation and the reason.
 
 ## 7. zathras
 
-zathras is the first process of the app container. It runs the service,
-keeps it running, and tells draal.
+zathras is the first process of the pod's container. It mounts the store
+of its service (7.8), runs the service, keeps it running, and tells
+draal.
 
 ### 7.1 Running the service
 
 - The service is started with the environment and the working directory of
-  zathras, no standard input, and a process group of its own.
+  zathras, no standard input, and a process group of its own. It is
+  started by the launcher (7.8): without `CAP_SYS_ADMIN` and a way to gain
+  it, as the user of `--user`, and through the program's own
+  `exec-in-place`, never straight from the mount.
 - **`activate`**: zathras checks that the binary is a file it may execute,
   and refuses otherwise, leaving what runs alone. Then it takes the
   generation as its own, stops the service if one runs, forgets any backoff
@@ -457,7 +491,9 @@ keeps it running, and tells draal.
   ran for 30 seconds or longer, at once. If it ended sooner, after a delay:
   1 second the first time, then twice as long each time, up to 2 minutes. A
   run of 30 seconds or longer makes the next delay 1 second again.
-- A binary that cannot be started counts as a service that ended at once.
+- A binary that cannot be started counts as a service that ended at once:
+  `exec-in-place` says why on the service's standard error and ends with
+  127 or 126.
 
 ### 7.2 As the first process
 
@@ -467,16 +503,20 @@ keeps it running, and tells draal.
   SIGTERM, SIGINT and SIGHUP on to it and wait for every child there is.
   The zathras that does the work is that child, and the container ends
   with its exit status.
-- On SIGTERM, SIGINT or SIGHUP it stops the service as in 7.1, ends the shells and
-  exits with 0. It starts nothing while it does. The pod's
-  `terminationGracePeriodSeconds` has to be longer than `--stop-timeout`.
+- On SIGTERM, SIGINT or SIGHUP it stops the service as in 7.1, ends the shells,
+  unmounts the store and exits with 0. It starts nothing while it does.
+  The pod's `terminationGracePeriodSeconds` has to be longer than
+  `--stop-timeout`.
 
 ### 7.3 Preparing
 
-`prepare` posts the store paths of the closure, one on a line, to the
-preload endpoint. An answer of 200 is followed by the check of the binary;
-any other answer is the error, with its body. draal ends a `prepare` that
-has outlived the rollout by closing its stream, which ends the request.
+`prepare` has the mount fetch the store paths of the closure, in the
+process. The first that cannot be fetched ends the others and is the
+error, which names the path and says whether the store has none of that
+name for the service or it could not be fetched. All of them fetched is
+followed by the check of the binary. draal ends a `prepare` that has
+outlived the rollout by closing its stream, which ends the fetching.
+With `--no-store` nothing is fetched and the binary alone is checked.
 
 ### 7.4 Logs
 
@@ -506,15 +546,16 @@ Every 2 seconds zathras reads, on Linux with cgroup v2:
 | disk read and written, in bytes per second | `rbytes` and `wbytes` of `io.stat`, over all devices |
 | network received and sent, in bytes per second | `/proc/net/dev`, over all interfaces but the loopback |
 
-The figures are those of the container, which is the service, zathras and
-the shells. A sample goes out with the next report. draal keeps the last 15
+The figures are those of the container, which is the service, zathras
+with the store it serves, and the shells. A sample goes out with the next report. draal keeps the last 15
 minutes of every connected zathras in memory. Where a file is missing
 (cgroup v1, another system) there are no samples, and the UIs say so.
 
 ### 7.6 Shell
 
 `shell` starts `--shell` on a pseudo-terminal, in a session of its own, with
-the environment of zathras and the `TERM` of the request. On its stream the
+the environment of zathras and the `TERM` of the request, by the launcher
+as the service is started (7.1). On its stream the
 asking side sends input and changes of size, and zathras sends output and,
 when the shell has ended, its exit status. A stream that ends ends the
 shell: SIGHUP to its process group, then SIGKILL. That holds whatever
@@ -523,7 +564,8 @@ input, with more typed than the stream holds, ends like any other when
 the asking side closes, on draal and in the container.
 
 A shell is in the container of zathras: it sees the store, the files and
-the processes the service sees.
+the processes the service sees, as the user of the service and with the
+capabilities of the service, `CAP_SYS_ADMIN` not among them.
 
 ### 7.7 Health check
 
@@ -547,6 +589,33 @@ every address: the kubelet asks at the address of the pod, not on loopback.
   kubelet restart the container of a service that stays down, and with the
   container go the backoff of zathras, its ring of lines and its identity.
 
+### 7.8 The store and the launcher
+
+[The design of stage 4](2026-10-11-great-machine-stage-4-design.md) has both in full (§4 and §5). In short:
+
+- **The mount.** zathras runs jaccard-nfs-nix-store's NFS server in its
+  own process, on a loopback port the system picks, and mounts it on
+  `/nix/store` of its own container, with the prefix of its service. A
+  store path is pulled from draal's store the first time it is named, by
+  a key of the mount's own, and kept in `--store-cache`. A zathras that
+  cannot mount ends at start. On its way out it unmounts.
+- **The container** has `CAP_SYS_ADMIN` and no AppArmor profile, and is
+  not privileged.
+- **The launcher** is one thread of zathras, which has dropped
+  `CAP_SYS_ADMIN` from its bounding set and set `no_new_privs`. The
+  service and every shell are started from it, and so inherit both; with
+  `--user` they are that user, without any capability, and are given the
+  pipes and the terminal they are started on, so that they can open
+  `/dev/stdout` or their terminal anew.
+- **Nothing is executed from the mount by zathras itself.** A program
+  on the mount is read through the NFS server of zathras, and Go's way to
+  start a process holds the whole of zathras until the program is read:
+  the two would wait for each other. What the launcher starts is the
+  program itself, `great-machine exec-in-place DIR PATH ARGV0 [ARG...]`,
+  which changes to `DIR`, if there is one, and then replaces itself with
+  the program at `PATH`. The working directory is that process's to
+  change to for the same reason: it may be in the store.
+
 ## 8. draal
 
 ### 8.1 What it keeps
@@ -564,6 +633,11 @@ A service gets its row with its first rollout. In memory only: the
 zathrases that are connected, the last report of each and 15 minutes of
 its samples. A zathras that has gone is forgotten.
 
+Beside it, in `store.sqlite`, is what jaccard-store's server keeps of the
+store: the references, the packs and which pack leans on which, and the
+uploads under way. The packs are in the bucket. That database is the only
+record of the store ([stage 4](2026-10-11-great-machine-stage-4-design.md), §3.2).
+
 draal is meant for a fleet of some hundreds of zathrases: the fleet is sent
 whole to everyone who watches.
 
@@ -583,6 +657,7 @@ so the TUI and the web page cannot differ in what they are able to show.
 | `/api/zathras/{id}/history` | the samples |
 | `/api/zathras/{id}/logs` | WebSocket: the ring, then what follows |
 | `/api/zathras/{id}/shell?cols=&rows=&term=` | WebSocket: binary messages are input and output, text messages are JSON (a change of size one way, the exit status the other) |
+| `/store/` and what is under it | jaccard-store's admin page over the store's database: read-only |
 | everything else | the web page; a path that is no file is answered with `index.html`, but for one under `/assets/`, which is not found |
 
 The HTTP API can roll nothing out and nothing back. A rollout needs the Nix
@@ -608,6 +683,8 @@ It follows the Fables for Robots design system.
 
 The page holds one WebSocket to `/api/watch` and draws from what it
 receives. It says so when the connection is lost, and connects again.
+Its header has a link to the store's page, `/store/`, which is
+jaccard-store's own and not part of this application.
 
 The sources are in `ui/`, a project of its own that a `go.mod` of its own
 keeps out of the Go module. Its build is written to `web/dist`, which the
@@ -707,17 +784,19 @@ Every package is at the top level of the module; there is no `internal/`.
 | Package | Purpose | Depends on |
 |---|---|---|
 | `wire` | messages, framing, error codes | |
-| `node` | keys, the endpoint of draal, dialing draal; over the `node` package of jaccard-store where that fits | go-iroh |
+| `node` | keys, the endpoint of draal with the protocols it accepts, dialing draal; over the `node` package of jaccard-store where that fits | go-iroh |
 | `node/nodetest` | a network in memory, for tests without iroh | `node` |
 | `logring` | the ring of lines and its followers | `wire` |
 | `stats` | samples from cgroup v2 and `/proc/net/dev` | `wire` |
 | `reaper` | as process 1: runs the program as its child and waits for every child | |
 | `supervisor` | starts, stops and restarts the service; backoff | `logring`, `wire` |
 | `testservice` | a program for the tests: exits, hangs or ignores SIGTERM on request | |
-| `preload` | the client of the sidecar's preload endpoint | |
+| `launcher` | the thread that starts the service and the shells, and the program replacing itself with another | |
+| `storemount` | the store of a zathras: jaccard-nfs-nix-store's NFS server in the process, mounted on `/nix/store` | jaccard-nfs-nix-store, `node`, `wire` |
 | `ptyshell` | shell sessions on a pseudo-terminal | |
 | `health` | the HTTP health check | |
 | `zathras` | the init: the above, and the connection to draal | `wire`, `node` |
+| `storeserver` | the store in draal: jaccard-store's server, its database and bucket, answered on draal's endpoint with authorization; its admin page | jaccard-store, `authz`, `node` |
 | `db`, `db/dbq` | migrations, queries, what sqlc makes of them | |
 | `fleet` | the connected zathrases, their reports and samples | `wire` |
 | `rollout` | the reconciler | `db`, `fleet` |
@@ -725,7 +804,7 @@ Every package is at the top level of the module; there is no `internal/`.
 | `draal` | `Controller` and its implementation, the handler of the protocol | `db`, `fleet`, `rollout`, `authz`, `wire`, `node` |
 | `operator` | the client of draal for commands and TUI | `wire`, `node` |
 | `closure` | the closure of a store path, by `nix-store` | |
-| `push` | store paths to jaccard-store | jaccard-store, amber-store core |
+| `push` | store paths to the store | jaccard-store, amber-store core |
 | `web` | the HTTP API and the embedded page | `draal` |
 | `tui` | the terminal UI, over an interface that the client of `operator` satisfies | `wire`, `term`, `human` |
 | `tui/tuitest` | a pseudo-terminal for the tests that run the terminal UI as a whole; part of no program | |
@@ -745,7 +824,8 @@ zathrases report, and answers what is to be done.
 ## 12. Build and delivery
 
 - **Flake.** `flake.nix` with `.envrc` (`use flake`), as the sibling
-  repositories have. The shell has Go, sqlc and Node.
+  repositories have. The shell has Go, sqlc and Node. The Go is 1.27,
+  which the packages of the mount's NFS server (Buildbarn's) need.
 - **The Go build** is [gonixgo](https://github.com/draganm/gonixgo)'s
   `buildGoApplication`, with `CGO_ENABLED = 0`. It needs
   `--option allow-unsafe-native-code-during-evaluation true`.
@@ -757,19 +837,24 @@ zathrases report, and answers what is to be done.
 - **The image**, `ghcr.io/fables-for-robots/great-machine`, for amd64 and
   arm64, is made by Nix of the same derivation: the program, a static
   busybox for `/bin/sh` and the shell's tools, and CA certificates. Its
-  entrypoint is `great-machine`. It is the image of the app container of a
-  pod, and of draal. The pod mounts the sidecar's store over `/nix/store`,
-  so nothing in the image may be a link into its own store: the program,
-  busybox and the certificates are copied in as files. gonixgo does not
+  entrypoint is `great-machine`. It is the image of the container of a
+  pod, and of draal. zathras mounts the store over `/nix/store`, which the
+  image has empty, so nothing in the image may be a link into its own
+  store: the program, busybox and the certificates are copied in as
+  files. gonixgo does not
   cross-compile, so each architecture is built on a runner of its own and
   the two are joined under one tag.
-- **`deploy/pod.yaml`** is a whole pod: the sidecar as jaccard-nfs-nix-store
-  documents it, and the app container with `great-machine zathras`,
-  `/nix/store` mounted with `HostToContainer` propagation, and a readiness
-  probe on the health check of zathras.
-  **`deploy/draal.yaml`** runs draal with a volume for its data.
+- **`deploy/pod.yaml`** is a whole pod: one container with
+  `great-machine zathras`, `CAP_SYS_ADMIN` and no AppArmor profile, a
+  volume for the cache of the store, and a readiness probe on the health
+  check of zathras.
+  **`deploy/draal.yaml`** runs draal with a volume for its data, the
+  bucket with its credentials from a Secret, and a volume for the
+  scratch space of the store.
 - **CI.** `test.yml` runs vet, the tests, `sqlc diff` and the type check,
-  the unit tests and the build of the page. `release.yml`, on a tag `v*`,
+  the unit tests and the build of the page, and the tests that need root
+  and the kernel in a container that has what a pod's container has
+  (`scripts/test-linux.sh`). `release.yml`, on a tag `v*`,
   builds and pushes the image as `:<tag>` and `:latest` and makes a
   GitHub release.
 
@@ -782,8 +867,14 @@ zathrases report, and answers what is to be done.
 - **draal restarts during a rollout**: 6.2. The rollout goes on.
 - **A zathras restarts** (the container was restarted): it comes back
   `idle` with a new endpoint ID and is sent a generation as 6.2 says.
-- **The sidecar cannot fetch a path**: the `prepare` fails and the rollout
+- **A zathras cannot fetch a path**, or the store has none of that name
+  for the service: the `prepare` fails with the path and the rollout
   halts before anything was restarted.
+- **The mount cannot be made**: zathras ends at start and says what the
+  container needs.
+- **draal is down**: a pod serves what it has fetched, and a path it has
+  not is an I/O error for whoever names it, after the mount's own
+  timeout.
 - **The new binary crashes**: the rollout halts at the first zathras, which
   goes on restarting it with backoff until the operator rolls out or back.
 - **A shell or a log is asked of a zathras that is gone**: `unavailable`.
@@ -809,9 +900,17 @@ Tests use the package `testing` of the standard library.
   fake S3 bucket, as jaccard-nfs-nix-store's tests have it, with a store
   path that is a directory, one that is a file, and one that is there
   already.
-- **`e2e`**: draal and several zathrases in one process over go-iroh on the
-  loopback interface, with a fake preload endpoint and real child
-  processes: a rollout from the operator client to `completed`; one that
+- **`storeserver`, `launcher`, `storemount`** as [stage 4](2026-10-11-great-machine-stage-4-design.md) has them
+  (§12): the store over a bucket in memory and real endpoints; the
+  launcher and the mount on Linux as root, in a container that is not
+  privileged (`scripts/test-linux.sh`), a program started from the mount
+  300 times while the garbage collector runs among them.
+- **`e2e`**: draal, with its store over a bucket in memory, and several
+  zathrases in one process over go-iroh on the loopback interface, with a
+  fake in the place of the mount and real child processes: closures of
+  two services pushed through draal's endpoint ID and pulled again by a
+  stranger; on Linux as root, one rollout with no fake at all, into a
+  store that is mounted and fetched from draal's, started by a launcher; a rollout from the operator client to `completed`; one that
   halts on a binary that exits; logs, a shell and the fleet through the
   operator client and through HTTP; a peer that is no operator, refused;
   the health check of a zathras, which fails before the rollout, passes
@@ -869,18 +968,22 @@ The plan begins with these, as each can change something above:
    written, against jaccard-store v0.7.0: a single file is imported and
    pushed by the same calls as a directory, and comes back identical. A
    symbolic link cannot be pushed as a link, since the import follows it,
-   so `rollout` refuses a closure that has one (6.1). That the sidecar
-   serves a single-file reference was read in its design and its code,
-   and is still to be seen running on Linux.
+   so `rollout` refuses a closure that has one (6.1). That the mount
+   serves a single-file reference was seen running on Linux in stage 4:
+   as a regular file with its content, and with an execute bit whether or
+   not the file had one, since a root that is a file has no entry of a
+   directory to keep a mode in.
 
 ## 16. Order of work
 
-Three stages, each of which ends with something that works by itself:
+Four stages, each of which ends with something that works by itself:
 
 1. zathras, draal and the operator commands: a rollout from the laptop to
    pods, with `status`, `logs` and `shell` on the command line.
 2. The web page.
 3. The terminal UI.
+4. The store in draal and the mount in zathras: one endpoint ID, and a
+   pod of one container ([its design](2026-10-11-great-machine-stage-4-design.md)).
 
 ## 17. Not in this design
 
@@ -890,5 +993,6 @@ Three stages, each of which ends with something that works by itself:
   by whether its process runs, and for how long.
 - Environment and secrets of a service: they are those of the pod.
 - Rollouts and rollbacks from the web page or the TUI.
-- Removing a service from draal.
+- Removing a service from draal, and removing anything from the store.
+- Limiting who may pull from the store.
 - More than one draal for a fleet.
````

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/specs/2026-10-09-great-machine-design.md
git commit -m 'Docs: the design brought up to stage 4: one endpoint for two protocols, the store in draal, the mount and the launcher in zathras'
```

- [ ] **Step 7: The files are what Kubernetes and GitHub take**

```bash
nix shell nixpkgs#kubeconform nixpkgs#actionlint -c sh -c 'kubeconform -strict -summary deploy/pod.yaml deploy/draal.yaml && actionlint .github/workflows/test.yml .github/workflows/release.yml'
```

Expected: `Summary: 3 resources found in 2 files - Valid: 3, Invalid: 0, Errors: 0, Skipped: 0`, and nothing from `actionlint`.

- [ ] **Step 8: The module names a release**

```bash
grep -n 'jaccard-nfs-nix-store\|^replace' go.mod
```

Expected: one line, `github.com/amber-store/jaccard-nfs-nix-store v0.3.0`, and no `replace`. If it names a commit (Task 10 was done before the release existed): ask the human partner for the release (Task 1, its last step), then

```bash
nix develop .#bare --command bash -c 'go get github.com/amber-store/jaccard-nfs-nix-store@v0.3.0 && go mod tidy && go test ./storemount/ ./cli/'
scripts/test-linux.sh ./storemount/ ./cli/ ./e2e/
git add go.mod go.sum && git commit -m "Build: jaccard-nfs-nix-store v0.3.0"
```

- [ ] **Step 9: The program and the image, as a release builds them**

```bash
nix build --option allow-unsafe-native-code-during-evaluation true
./result/bin/great-machine version
scripts/test-page.sh ./result/bin/great-machine
rm result
```

Expected: the commit, and `ok`. On Linux, or where Nix has a Linux builder, the image as well, which is what `test.yml` does on every push:

```bash
nix build .#image --option allow-unsafe-native-code-during-evaluation true
docker load < result
docker run --rm --entrypoint sh ghcr.io/fables-for-robots/great-machine:nix \
  -c 'test -d /nix/store && test -z "$(ls -A /nix/store)" && test -d /var/cache/great-machine'
scripts/test-page.sh --image ghcr.io/fables-for-robots/great-machine:nix
docker image rm ghcr.io/fables-for-robots/great-machine:nix; rm result
```

Expected: `ok`. **Neither the image nor the Linux build was made while this plan was written.** Where it cannot be made here either, say so in the ledger: the first push of the branch makes it on GitHub.

- [ ] **Step 10: The whole, once more**

```bash
nix develop .#bare --command bash -c 'test -z "$(gofmt -l .)" && go vet ./... && CGO_ENABLED=0 GOOS=linux go vet ./... && go test -count=1 ./...'
scripts/test-linux.sh -count=1
scripts/test-pid1.sh
(cd ui && npm ci && npm run check && npm test && npm run build) && nix develop .#bare --command go test -count=1 ./web/... ./cli/ ./e2e/
git clean -fdxq web/dist ui
git grep -n -i -E 'sidecar|--jaccard-|JACCARD_|preload-url|9889' -- ':!docs/superpowers/plans' ':!docs/superpowers/specs/2026-10-11-great-machine-stage-4-design.md' | grep -v _test.go
git status --short
```

Expected: `ok` for the 33 packages on this machine and on Linux; `ok` from the script of process 1; the page's checks and tests pass and the three packages pass with the page built; the `grep` prints only what is meant: the sentences of the README and of the design that say that there is no sidecar any more and what v0.1.0 had, `deploy/pod.yaml`'s two comments to the same end, and package names of jaccard-nfs-nix-store in `storemount`; and nothing from `git status`.

- [ ] **Step 11: The pull request: ask**

**Stop here.** The branch is ready to be pushed, and nothing is pushed without the human partner's word. Tell them what the ledger holds as open (the Linux build by Nix and the image, if they were not made; that the first run of `test.yml` on GitHub is the first run of the mount under AppArmor), and that Task 14 is to be run on a cluster before or after the merge, as they choose. On their word:

```bash
git push -u origin stage-4
gh pr create --title "Stage 4: one endpoint, no sidecar" --body-file - <<'BODY'
draal is the store of the fleet, under its own endpoint ID, and a pod is one
container whose zathras mounts that store on /nix/store. No jaccard-store
server to run, no sidecar, no privileged container.

The design: docs/superpowers/specs/2026-10-11-great-machine-stage-4-design.md
The plan and the ledger of its execution: docs/superpowers/plans/2026-10-11-great-machine-stage-4.md

Upgrading from v0.1.0 is in the README: draal gets a bucket, every pod
becomes one container, and every service is rolled out once more.
BODY
```

and watch the first run of `test.yml`: its step "linux, as root" is the one that says whether `CAP_SYS_ADMIN` and no AppArmor profile are enough to mount on a kernel that has AppArmor.
