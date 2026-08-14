# UnpackFlow

UnpackFlow 是面向群晖 NAS 的轻量自动解压服务，基于 Unpackerr 解压核心，提供中文 WebUI、CloudDrive2 直连监控和通知功能。

## 功能

- 自动监控本地目录，支持 ZIP、RAR、7z、TAR、GZ、BZ2、XZ、ISO 和常见分卷。
- 本地监控同时使用实时文件事件与定期补偿扫描。
- 本地压缩包解压成功后可选择保留、删除或移动到归档目录。
- CloudDrive2 使用 gRPC-Web + Token 直连，新压缩包先复制到本地缓存，完整落盘后再解压。
- CD2 缓存和云端原包分别设置保留或删除，不影响本地监控目录的文件。
- 提供中文任务、历史、密码、通知、日志和设置界面。
- 仅发布 Docker 镜像，支持 `linux/amd64` 和 `linux/arm64`。

## 群晖部署

```yaml
services:
  unpackflow:
    image: ghcr.io/zaiwuli/unpackflow:latest
    container_name: unpackflow
    restart: unless-stopped
    environment:
      TZ: Asia/Shanghai
    volumes:
      - /volume2/解压目录:/data
      - /volume1/CloudNAS/CloudDrive:/volume1/CloudNAS/CloudDrive
      - /volume1/docker/unpackflow:/config
    ports:
      - 8066:5656
```

只需挂载一个本地数据目录。首次启动时会自动创建：

```text
/volume2/解压目录/
  监控目录/
  解压目录/
  缓存目录/
  归档目录/
```

容器内对应 `/data/监控目录`、`/data/解压目录`、`/data/缓存目录` 和 `/data/归档目录`。

旧版 `/downloads`、`/output`、`/cache` 独立挂载方式仍然兼容。新部署建议使用 `/data` 单目录挂载。

启动后访问：`http://群晖IP:8066`

## 设置说明

- “本地目录”可选择原包保留、删除或归档；补偿扫描默认每 `60s` 执行一次。
- `0s` 仅关闭定期补偿扫描，实时文件事件监听仍然开启。
- “CloudDrive2 直连”中的删除云端原包只作用于 CD2 缓存任务，不会删除本地监控目录中的文件。
- 设置保存后重启容器生效。

## 镜像

推送到 `main` 后，GitHub Actions 自动构建并发布：

```text
ghcr.io/zaiwuli/unpackflow:latest
```

## 许可证

MIT
