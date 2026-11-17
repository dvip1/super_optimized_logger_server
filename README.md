## Phase 1: Skeleton & Conformance (The "Hello World" phase)

*Goal: Get the server running, create the database, and pass the fast conformance checks.*

* **Setup the Project:** Initialize a standard Go module. Use the standard library `net/http` for routing (Go 1.22+ has excellent built-in routing) to avoid heavy frameworks. Add `[github.com/mattn/go-sqlite3](https://github.com/mattn/go-sqlite3)` (or `modernc.org/sqlite` for pure Go).
* **Implement the Database Schema:**
* Create the `readings` table with `WITHOUT ROWID` and `PRIMARY KEY (srno, ts)`.
* Create a `devices` table to map `srno` to `token`, `owner_id`, and `aes_key`.
* Create tables for `users` and `tokens`.


* **Build the Control Plane (Auth & Tokens):**
* Implement `/signup`, `/login`, and `/tokens`.
* Use a simple signed JWT or a random string stored in SQLite for the `access_token`.


* **Build Mock Device Endpoints:**
* Implement `/health` (just return `200 OK`).
* Implement `/register` (generate the AES key, save to DB, return it).
* Implement a dummy `/data` that just returns `200 OK` (we will build the real one in Phase 2).


* **Milestone Test:** Run `go run . -url http://localhost:8080 -devices 0`. Do not move to Phase 2 until this passes.

## Phase 2: The Core Engine (Ingest & Group Commit)

*Goal: Handle the massive write load safely without destroying the SSD or dropping data.*

* **Build the Hot-Path RAM Maps:**
* Create a `map[string]*DeviceState` holding the AES key, owner, revoked status, and `lastTimestamp`.
* Load this map from SQLite on boot. Updates to this map (like a new registration) must be protected by a `sync.RWMutex`.


* **Implement the Group Commit Channel:**
* Create a Go channel that accepts a struct containing the decrypted reading, the `srno`, and a `chan error` (the wait channel).
* Create a single "Writer Goroutine" that reads from this channel. Have it collect items into a slice until it hits ~500 items *or* a 50ms timer ticks.
* The Writer Goroutine starts a single SQLite transaction, executes `INSERT` for all items, commits, and then sends `nil` down all 500 wait channels.


* **Write the `/data` Handler:**
* Use `sync.Pool` to allocate a 301-byte buffer to read the incoming HTTP body (prevents GC spikes).
* Look up the device in the RAM map. Decrypt the AES-128-GCM payload.
* Check if the timestamp is strictly greater than `lastTimestamp`.
* Send the payload to the Group Commit channel and block on the wait channel. When the wait channel returns, send `200 OK`.


* **Milestone Test:** Run a small live load: `go run . -url http://localhost:8080 -devices 500 -tokens <your_token>`.

## Phase 3: The Read Path & Float Exactness

*Goal: Serve data back flawlessly so the audit passes.*

* **Build the `/latest` Handler:**
* Create a second RAM map: `map[string][]byte` (or array of 64 floats) that stores the absolute latest reading per device.
* Update this map inside your Group Commit writer just before it replies to the wait channels.
* The `/latest` endpoint just reads from this map and formats the JSON. No DB lookup required.


* **Build the `/readings` Handler:**
* Query the SQLite `readings` table using `WHERE srno = ? AND ts >= ? AND ts <= ? ORDER BY ts ASC LIMIT ?`.
* **Crucial Float Step:** Parse the 256-byte BLOB directly into an array of 64 `float32`s using `math.Float32frombits`.
* Write a custom JSON encoder (or use `strconv.FormatFloat(float64(f), 'g', -1, 32)`) to ensure the floats are printed perfectly without floating-point expansion errors.


* **Milestone Test (Crash Recovery):** Run load with 1,000 devices. In another terminal, run `kill -9 <pid>`. Restart the server. Ensure the judge outputs `PASS` for the audit.

## Phase 4: Frontend & Dashboard

*Goal: Build a UI that sets you apart from the terminal-only submissions.*

* **Setup `embed.FS`:** Create a `static` folder with an `index.html`. Tell Go to embed this directory into the final binary and serve it on `/`.
* **Design the SPA:** Use Vanilla JS, Preact, or Vue (no complex build step required if you use CDN links or pre-compiled files). Build the login screen, token generation, and a list of devices.
* **Implement Live Updates (SSE):**
* Create an endpoint like `/api/v1/stream`.
* Use Server-Sent Events (SSE). Have your ingest engine fan out the `latest` readings to connected web clients once a second.
* Plot the data using a lightweight charting library like uPlot or Chart.js.


* **Milestone Test:** Create tokens in your own UI. Run the judge in "load only" mode: `go run . -devices 1000 -tokens <token1>,<token2>`. Watch your dashboard light up.

## Phase 5: Scale, Tune, & Benchmark

*Goal: Squeeze every ounce of performance out of the 2 vCPUs.*

* **Run the Profiler:** Add `net/http/pprof` to your router. Run a heavy load test and look at the CPU profile. If Garbage Collection (GC) is taking more than 5% of your CPU, hunt down and pool your allocations.
* **Tune SQLite Pragmas:** Ensure your startup routine executes:
* `PRAGMA journal_mode = WAL;`
* `PRAGMA synchronous = NORMAL;` (or `FULL` if you want to be ultra-safe, test both).
* `PRAGMA temp_store = MEMORY;`


* **Write the Setup Script:** Create a simple `install.sh` that installs Go, downloads your repo, builds the binary, and sets up a systemd service file so it restarts automatically on failure.
* **Final Benchmark:** Run `go run . -url http://<server>:8080 -devices 5000 -duration 5m -crash-test` on the actual AWS Lightsail box. Note your metrics for the README.
