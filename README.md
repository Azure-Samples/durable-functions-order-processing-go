# Durable Functions Go Order Processing

This sample deploys a Go Durable Functions app to Linux Flex Consumption using
Azure Developer CLI (`azd`) and **Durable Task Scheduler**. It uses published Go
worker and Durable middleware **v1.0.0**.

`ProcessOrder` reserves inventory, waits for a `PaymentReceived` external event,
and ships a paid order. If payment is declined or the two-minute durable timer
expires, it releases inventory and completes with status `cancelled`.
Custom status exposes each step. Reservations and shipments are **simulations**:
no real payment, inventory, or delivery service is called.

The middleware depends on `durabletask-go v1.0.0-beta3` and requires DurableTask
extension **3.15.0 or later**. This sample uses the experimental extension bundle
recommended by the [GA middleware documentation](https://github.com/Azure/azure-functions-golang-worker/tree/v1.0.0/middleware/durabletask).
The GA module tags do not remove these upstream/bundle constraints.

## Prerequisites

- [Go 1.25 or later](https://go.dev/dl/)
- [Azure Functions Core Tools 4.12 or later](https://learn.microsoft.com/azure/azure-functions/functions-run-local)
- [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli)
- [Azure Developer CLI](https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd)
- Docker and [Azurite](https://learn.microsoft.com/azure/storage/common/storage-use-azurite) for local testing
- [PowerShell 7](https://learn.microsoft.com/powershell/scripting/install/installing-powershell) for the commands shown below
- An Azure subscription with permission to create resources and role assignments

## Run locally

```powershell
git clone https://github.com/Azure-Samples/durable-functions-order-processing-go.git
cd durable-functions-order-processing-go
Copy-Item local.settings.example.json local.settings.json
```

Start the scheduler emulator in a separate terminal:

```powershell
docker run --rm -p 127.0.0.1:8080:8080 -p 127.0.0.1:8082:8082 -e DTS_TASK_HUB_NAMES=GoOrders mcr.microsoft.com/dts/dts-emulator:latest
```

Start Azurite in another terminal:

```powershell
azurite --silent --skipApiVersionCheck --location .azurite
```

The emulator needs `DTS_TASK_HUB_NAMES=GoOrders`; its default hub is named
`default`. Azurite serves host storage only. The scheduler stores orchestration
history. The unauthenticated scheduler connection is local-only.

Start the app:

```powershell
if ($IsWindows) {
    $env:languageWorkers__native__defaultExecutablePath = Join-Path (Get-Location) 'bin\app.exe'
}
func start
```

The process-only override works around Core Tools 4.12's Windows native worker
executable path; it is not part of the Linux deployment configuration.

Submit an order and confirm payment before the timeout:

```powershell
$start = Invoke-RestMethod -Method Post -Uri http://localhost:7071/api/orders -ContentType application/json -Body '{"id":"order-001","item":"coffee","quantity":2}'
Invoke-RestMethod -Method Post -Uri "http://localhost:7071/api/orders/$($start.id)/payment" -ContentType application/json -Body '{"paid":true}'
Invoke-RestMethod -Uri $start.statusQueryGetUri
```

Poll until `runtimeStatus` is `Completed`. Expected output:

```json
{"orderId":"order-001","status":"shipped","tracking":"demo-order-001"}
```

Submit another order and send `{"paid":false}` to test compensation, or omit
payment and wait two minutes to test the durable timeout. Both paths complete
with an output status of `cancelled` after `ReleaseInventory`. A workflow that
business logic cancels still has a Durable runtime status of `Completed`.

`GET /api/orders/{instanceId}` exposes runtime/custom status and output.
The starter returns HTTP 202 with management URLs, and payment returns HTTP 202.
Missing instances return 404; payment for a completed order returns 409.
The emulator dashboard is at `http://localhost:8082`.

## Deploy to Azure

```powershell
az login
azd auth login
azd env new go-orders
azd env set VNET_ENABLED false
azd up
```

Choose a region supporting both Flex Consumption and
[Durable Task Scheduler](https://learn.microsoft.com/azure/durable-task/scheduler/durable-task-scheduler).
Infrastructure provisions a Consumption scheduler and task hub, function app,
storage, monitoring, and user-assigned managed identity. The identity has
**Durable Task Data Contributor** at task hub scope. The scheduler connection
uses `DefaultAzure`; host storage uses managed identity with shared keys disabled.
Scheduler connection and dashboard URL are exported as `azd` outputs.

HTTP endpoints require function keys in Azure; use the `x-functions-key` header.
Local Core Tools does not enforce keys by default. Management URLs can include
host system keys and must not be shared. For production, authenticate and
authorize payment notifications through a trusted provider; a function-key
protected simulated payment endpoint is not a payment integration.

The optional VNet setting privatizes host storage only. Scheduler access remains
on its public authenticated endpoint. The scheduler IP allowlist permits all IPv4
addresses; Microsoft Entra authentication and task hub RBAC remain required.
Restrict the allowlist or use private networking for production. Resources and
telemetry can incur charges; clean up with `azd down`.

## Development and limitations

```powershell
go test ./...
go vet ./...
```

There is no `function.json`: middleware registers the orchestrator and activities
with the worker. All I/O belongs in activities, and orchestrator code must remain
deterministic during replay.

Real activities must be idempotent and implement appropriate retries and
compensation for service failures. Duplicate order submissions currently create
independent instances; duplicate/conflicting payment events are not a payment
ledger. The two-minute timeout is intentionally short for the demo. Unit tests
cover request validation and simulated activities, not real orchestration timing
or external service guarantees.
