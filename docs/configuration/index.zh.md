# 引言

sing-box 使用 JSON 作为配置文件格式。
### 结构

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

### 字段

| Key            | Format                 |
|----------------|------------------------|
| `$schema`      | [JSON Schema](./schema/) |
| `log`          | [日志](./log/)           |
| `dns`          | [DNS](./dns/)          |
| `ntp`          | [NTP](./ntp/)          |
| `certificate`  | [证书](./certificate/)   |
| `certificate_providers` | [证书提供者](./shared/certificate-provider/) |
| `http_clients` | [HTTP 客户端](./shared/http-client/) |
| `network_namespaces` | [网络命名空间](./network-namespace/) |
| `endpoints`    | [端点](./endpoint/)      |
| `inbounds`     | [入站](./inbound/)       |
| `outbounds`    | [出站](./outbound/)      |
| `route`        | [路由](./route/)         |
| `services`     | [服务](./service/)       |
| `experimental` | [实验性](./experimental/) |

### 检查

```bash
sing-box check
```

### 自动重载

`sing-box run` 默认监控配置文件内容，每 500 毫秒检查一次，连续两次内容相同时触发重载。
支持 `-c` 指定的文件、`-C` 目录内 JSON 文件的修改、添加和删除，以及默认的 `config.json`。
标准输入配置只读取一次；与文件混用时，重载会复用已读取的标准输入内容。

```bash
sing-box run -c config.json
sing-box run -C config.d
sing-box run --watch=false -c config.json
```

新配置读取、解析或实例创建失败时，继续运行原配置；新实例启动失败时，尝试恢复最近成功运行的配置。
重载会关闭并重新创建服务实例，已有连接会中断，流量统计也会重置。
Linux/macOS 仍可通过 `SIGHUP` 手动重载，包括使用 `--watch=false` 时。
自动监控只适用于命令行 `run`，不适用于使用 libbox 的客户端。

### 格式化

```bash
sing-box format -w -c config.json -D config_directory
```

### 合并

```bash
sing-box merge output.json -c config.json -D config_directory
```
