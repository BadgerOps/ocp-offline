# HTTP API

Routes are defined in `internal/server/server.go`.

## UI Pages

- `GET /` -> redirects to `/dashboard`
- `GET /dashboard`
- `GET /providers`
- `GET /providers/{name}`
- `GET /sync`
- `GET /transfer`
- `GET /ocp/clients`
- `GET /static/*` (embedded static assets)

## Core API

- `GET /api/status` - provider status summary
- `GET /api/providers` - active registered providers
- `POST /api/sync` - start sync (`provider` or `all`)
- `POST /api/sync/cancel` - cancel active sync/push operation
- `GET /api/sync/progress` - current progress snapshot/stream payload
- `GET /api/sync/running` - whether sync/push is active
- `POST /api/scan` - scan local files into store records
- `POST /api/validate` - validate provider content

## Failed Download Management

- `GET /api/sync/failures` - list unresolved failed files
- `DELETE /api/sync/failures/{id}` - resolve one failed file
- `POST /api/sync/failures/resolve` - bulk resolve failures
- `POST /api/sync/retry` - retry failed downloads

## Provider Config Management

- `GET /api/providers/config`
- `POST /api/providers/config`
- `PUT /api/providers/config/{name}`
- `DELETE /api/providers/config/{name}`
- `POST /api/providers/config/{name}/toggle`

## Transfer API

- `POST /api/transfer/export`
- `POST /api/transfer/import`
- `GET /api/transfers`

## Mirror Discovery API

- `GET /api/mirrors/epel/versions`
- `GET /api/mirrors/epel?version=<int>&arch=<arch>`
- `GET /api/mirrors/ocp/versions`
- `POST /api/mirrors/speedtest`

## OCP Client Discovery/Download API

- `GET /api/ocp/tracks`
- `GET /api/ocp/releases?channel=<channel>`
- `GET /api/ocp/artifacts?version=<version>`
- `POST /api/ocp/download`

## Registry Push API

- `POST /api/registry/push`

## Notes

- Several endpoints support HTMX form requests in addition to JSON.
- Long-running sync/push operations are asynchronous and update shared progress state.
