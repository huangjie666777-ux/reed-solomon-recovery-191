# 离线 Reed–Solomon 纠删恢复服务

使用 Go 1.27.1 与 Chi 5.2.1 实现的纯后端服务。纠删码、GF(256)、矩阵求逆和 ZIP 处理均在本仓库实现；不使用纠删码库，不访问云存储，也不在请求之间保留状态或临时文件。

## 启动

```bash
go run ./cmd/server
```

默认监听 `:8080`，可用 `ADDR=127.0.0.1:18080` 覆盖。

## 接口

### `POST /encode`

`multipart/form-data` 字段：

- `file`：1 字节到 1 MiB 的原始文件。
- `k`：数据片数量，范围 `2` 到 `8`。
- `m`：校验片数量，范围 `1` 到 `4`。

返回 `encoded.zip`，最多包含 13 个普通文件：

- `manifest.json`
- `shard-0.dat` ... `shard-{k+m-1}.dat`

### `POST /recover`

请求体直接上传由编码 ZIP 删除若干分片得到的 ZIP。解压总量不得超过 4 MiB，最多 13 个条目，且只能包含清单和合法分片名。

成功返回 `recovered.zip`，包含：

- 原始文件名对应的恢复文件。
- `report.json`：恢复状态、缺失编号、坏片编号、实际采用编号、原始与恢复摘要。

不足 k 个好片、矩阵不可逆或恢复文件 SHA-256 与清单不符时返回 HTTP 422 JSON 报告，不返回伪文件。非法清单、重复 ZIP 条目、非法编号、路径越界等返回 HTTP 400。

## manifest.json

```json
{
  "format_version": "1.0",
  "k": 3,
  "m": 2,
  "original_length": 100,
  "shard_length": 34,
  "original_name": "payload.bin",
  "original_sha256": "...",
  "shard_hashes": [
    {"number": 0, "sha256": "..."}
  ]
}
```

SHA-256 使用小写十六进制。恢复时，分片必须同时满足记录片长和分片 SHA-256 才会被采用；编号按编号匹配，不依赖 ZIP 条目顺序。重复 ZIP 路径、重复分片编号或同一编号的多份拷贝不能凑够 k 片。

## 算法

GF(256) 使用本原表示，约化多项式为 `x^8+x^4+x^3+x^2+1`，即 `0x11d`。

构造 k+m 行、k 列的 Vandermonde 矩阵：

```text
V[i][j] = (i+1)^j
```

编号和指数均从 0 开始。取 V 的前 k 行求逆，然后右乘形成系统生成矩阵：

```text
G = V · inverse(V[0:k])
```

因此 G 的前 k 行是单位矩阵，前 k 个编码片保持原始连续字节片。原始数据按向上取整片长切成 k 片，最后一片尾部补零。恢复时从任意 k 个唯一合法编号取对应 G 行，使用自行实现的 GF256 Gaussian–Jordan 消元求逆，再左乘可用片。重建结果先按 `original_length` 裁掉补零，再校验原始 SHA-256。

## 安全与边界

- 原始上传文件：1 字节到 1 MiB；参数范围 `2 <= k <= 8`、`1 <= m <= 4`。
- 编码和解码请求：上传和解压总量均限制为 4 MiB；ZIP 最多 13 个条目。
- 文件名和 ZIP 条目必须是单一路径组件，拒绝绝对路径、反斜杠、NUL、`..` 等路径越界形式。
- 摘要只能证明重建结果与调用方提供的清单一致，不能证明清单或文件来源可信。
- 服务完全离线；恢复只依赖当前请求中的清单和分片。

## curl 示例

```bash
printf 'hello reed-solomon offline recovery' > payload.txt
curl -sS -o encoded.zip -F file=@payload.txt -F k=3 -F m=2 http://127.0.0.1:8080/encode
python3 -c "from zipfile import ZipFile; print(ZipFile('encoded.zip').namelist())"

python3 - <<'PY'
from zipfile import ZipFile
with ZipFile('encoded.zip') as source, ZipFile('partial.zip', 'w') as target:
    for name in ('manifest.json', 'shard-2.dat', 'shard-3.dat', 'shard-4.dat'):
        target.writestr(name, source.read(name))
PY
curl -sS -o recovered.zip --data-binary @partial.zip http://127.0.0.1:8080/recover
python3 -c "from zipfile import ZipFile; print(ZipFile('recovered.zip').namelist())"

python3 - <<'PY'
from zipfile import ZipFile
with ZipFile('encoded.zip') as source, ZipFile('unrecoverable.zip', 'w') as target:
    for name in ('manifest.json', 'shard-0.dat', 'shard-1.dat'):
        target.writestr(name, source.read(name))
PY
curl -sS -i --data-binary @unrecoverable.zip http://127.0.0.1:8080/recover
```

## 测试

```bash
go test ./...
```

测试覆盖 GF256 逆元、矩阵求逆、系统矩阵、所有合法 k/m 下任意 k 片恢复，以及坏片、缺片、摘要篡改、重复 ZIP 条目和路径越界。
