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
- 115 自动发现直接分页扫描已配置的云目录，不依赖“生活记录”；自动扫描最短间隔为 `30m`，手动扫描可随时执行。
- 设置保存后重启容器生效。

## 任务与历史

- 当前任务包含下载、解压、自动重试和等待批准；最终失败、取消和忽略记录在历史中查看。
- 删除记录或清空历史只隐藏展示，保留成功防重复和忽略规则，不删除实际文件。
- 失败重试按阶段执行：完整缓存复用，云端移动和原包清理单独重试，不重新解压已完成的任务。
- 取消执行中的任务会先显示“正在取消”；已经提交给解压工具或云端的操作需等待返回后确认，不保证立即中止。
- 取消忽略只解除屏蔽，不直接提交任务。缺少来源信息的旧记录会提示重新同步，不猜测文件路径。

## 镜像发布

推送到 `main` 后，GitHub Actions 自动构建并发布：

```text
ghcr.io/zaiwuli/unpackflow:latest
```

## 许可证

MIT
