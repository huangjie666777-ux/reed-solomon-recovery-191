# 离线 Reed-Solomon 纠删恢复服务（纯后端）

Go 1.27.1 + Chi v5.2.1 实现。全部计算离线完成，不连接云存储、不访问外部网络。

## 接口

### `POST /encode`
`multipart/form-data`：
- `file`：1 字节至 1 MiB 的任意文件。
- `k`：数据片数，整数 `2..8`。
- `m`：校验片数，整数 `1..4`。

返回 `application/zip`（建议保存为 `encoded.zip`），共 `k+m+1` 个条目：
- `manifest.json`：清单。
- `shard-00` … `shard-(k+m-1)`：编号分片，名称固定两位数字。

```bash
curl -sS -o encoded.zip -F file=@photo.dat -F k=4 -F m=2 http://localhost:8080/encode
```

### `POST /recover`
`multipart/form-data`：
- `archive`：一个 ZIP，必须保留**完整的** `manifest.json`，并保留任意若干编号分片；可删除部分片，片内容也可以损坏。

成功（200）返回 ZIP，含：
- 原文件（名称取自清单 `original_name`）。
- `report.json`：恢复报告。

无法恢复（422）返回 `report.json` 内容（JSON，非 ZIP），**不会输出任何伪文件**。请求本身非法（坏 ZIP、非法清单、重复条目、路径越界、出现意外条目等）返回 400。

```bash
curl -sS -o recovered.zip -F archive=@damaged.zip http://localhost:8080/recover
```

## manifest.json 格式
```json
{
  "format_version": 1,
  "k": 4,
  "m": 2,
  "original_length": 1600,
  "shard_length": 400,
  "original_name": "photo.dat",
  "original_sha256": "…64 位小写十六进制…",
  "shards": [
    {"number": 0, "name": "shard-00", "sha256": "…"}
  ]
}
```
`report.json` 含 `status`（`success`/`unrecoverable`）、`k`、`m`、`missing_shards`、
`corrupt_shards`、`used_shards`、`recovered_sha256`、`error` 以及固定的来源可信说明。

## 算法
- 有限域 GF(2^8)，本原多项式 x^8+x^4+x^3+x^2+1（`0x11d`），自实现 log/exp 表及乘、除、逆元、幂。
- 范德蒙德矩阵 V 为 `(k+m)×k`：`V[i][j] = (i+1)^j`，行、列索引从 0 起，共使用域元素 1..k+m。
- 取 V 的前 k 行求逆（自实现 Gauss-Jordan 消元），令系统生成矩阵 `G = V · (V_top)^-1`：前 k 行为单位阵，因此前 k 片就是原始数据；其余 m 行为校验片。
- G 的任意 k 行构成可逆矩阵，所以任意 k 片即可恢复。恢复时对所选行求逆，逐字节解出数据片，按 `original_length` 裁掉尾部补零，并核对原文件 SHA-256。
- 文件按连续字节切成 k 个等长数据片，最后一片尾部补零（`shard_length = ceil(original_length/k)`）。

## 安全与边界
- 上传文件 1 B..1 MiB；上传/解压的**未压缩总量**上限均为 4 MiB，归档最多 13 个条目。
- 解压时按未压缩大小计数（含超限读取保护），拒绝非普通文件、绝对路径、盘符、`..` 穿越、分隔符、控制字符、保留设备名与重名条目。
- 恢复只接受 `manifest.json` 与 `shard-NN`；编号必须在 `[0,k+m)` 内且唯一，重复片不得凑足 k 片；处理不依赖条目顺序。
- 坏片按“片长不等或 SHA-256 不符”排除并列入 `corrupt_shards`；缺失片列入 `missing_shards`。
- 不足 k 片、矩阵不可逆或重建后原文件摘要不符：返回不可恢复报告，不返回文件。
- 摘要只能证明字节与清单一致，**不证明清单或文件来源可信**（报告中固定声明）。
- 请求相互独立；全部数据在内存中处理，不创建持久临时资源，无云存储依赖。

## 构建与测试
```bash
go test ./...
go build ./...
ADDR=:8080 go run ./cmd/server
```
