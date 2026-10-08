!!! quote ""

    默认构建包含 V2Ray API。使用 `without_v2ray_api` 构建标记可排除它，参阅 [安装](/zh/installation/build-from-source/#构建标记)。

### 结构

```json
{
  "listen": "127.0.0.1:8080",
  "stats": {
    "enabled": true,
    "inbounds": [
      "socks-in"
    ],
    "outbounds": [
      "proxy",
      "direct"
    ],
    "users": [
      "sekai"
    ]
  }
}
```

### 字段

#### listen

gRPC API 监听地址。如果为空，则禁用 V2Ray API。

#### stats

流量统计服务设置。

#### stats.enabled

启用统计服务。

#### stats.inbounds

统计流量的入站列表。

#### stats.outbounds

统计流量的出站列表。

#### stats.users

统计流量的用户列表。
