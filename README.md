# UnpackFlow

UnpackFlow 是面向群晖 NAS 的轻量自动解压服务，基于 Unpackerr 解压核心，提供中文 WebUI、CloudDrive2 直连监控和通知功能。

## 功能

- 监控本地目录，自动识别并解压 ZIP、RAR、7z、TAR、GZ、BZ2、XZ、ISO 及常见分卷。
- CloudDrive2 使用 gRPC-Web + Token 直连，不依赖 Webhook。
- CD2 压缩包先复制到本地缓存，复制完成后才开始解压。
- 支持设置 CD2 监控路径、路径映射、定时刷新和缓存保留策略。
- 中文任务页、历史记录、密码、通知、设置和日志页面。
- 支持群辉通知插件 / MoviePilot Webhook。
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
      - /volume2/解压目录/监控目录:/downloads
      - /volume2/解压目录/输出目录:/output
      - /volume2/解压目录/缓存目录:/cache
      - /volume1/CloudNAS/CloudDrive:/volume1/CloudNAS/CloudDrive
      - /volume1/docker/unpackflow:/config
    ports:
      - 8066:5656
```

启动后访问：`http://群晖IP:8066`

首次使用时，在“设置”页面填写 CloudDrive2 地址、Token 和监控路径；在“通知”页面填写通知地址并测试。

## 目录说明

- `/downloads`：本地监控目录。
- `/output`：解压输出目录。
- `/cache`：CD2 本地缓存目录。
- `/config`：配置、密码、历史和日志文件。

## 构建状态

推送到 `main` 分支后，GitHub Actions 会自动构建并发布：

```text
ghcr.io/zaiwuli/unpackflow:latest
```

## 许可证

MIT
