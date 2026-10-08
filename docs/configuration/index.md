# Introduction

sing-box uses JSON for configuration files.
### Structure

```json
{
  "$schema": "https://sing-box.sagernet.org/schema.json",
  "log": {},
  "dns": {},
  "ntp": {},
  "certificate": {},
  "certificate_providers": [],
  "http_clients": [],
  "network_namespaces": [],
  "endpoints": [],
  "inbounds": [],
  "outbounds": [],
  "route": {},
  "services": [],
  "experimental": {}
}
```

### Fields

| Key            | Format                          |
|----------------|---------------------------------|
| `$schema`      | [JSON Schema](./schema/)        |
| `log`          | [Log](./log/)                   |
| `dns`          | [DNS](./dns/)                   |
| `ntp`          | [NTP](./ntp/)                   |
| `certificate`  | [Certificate](./certificate/)   |
| `certificate_providers` | [Certificate Provider](./shared/certificate-provider/) |
| `http_clients` | [HTTP Client](./shared/http-client/) |
| `network_namespaces` | [Network Namespace](./network-namespace/) |
| `endpoints`    | [Endpoint](./endpoint/)         |
| `inbounds`     | [Inbound](./inbound/)           |
| `outbounds`    | [Outbound](./outbound/)         |
| `route`        | [Route](./route/)               |
| `services`     | [Service](./service/)           |
| `experimental` | [Experimental](./experimental/) |

### Check

```bash
sing-box check
```

### Automatic reload

`sing-box run` watches configuration contents by default. It checks every 500 milliseconds and reloads after two identical snapshots.
This includes files specified with `-c`, changes, additions and removals of JSON files in `-C` directories, and the default `config.json`.
Standard input is read once and reused when reloading configurations that also include files.

```bash
sing-box run -c config.json
sing-box run -C config.d
sing-box run --watch=false -c config.json
```

If reading, parsing or creating the new instance fails, the existing configuration keeps running.
If starting the new instance fails, sing-box attempts to restore the last successfully running configuration.
Reloading closes and recreates the service instance, interrupting existing connections and resetting traffic statistics.
On Linux/macOS, `SIGHUP` still triggers a manual reload, including when `--watch=false` is used.
Automatic watching applies only to the command-line `run` command, not clients using libbox.

### Format

```bash
sing-box format -w -c config.json -D config_directory
```

### Merge

```bash
sing-box merge output.json -c config.json -D config_directory
```
