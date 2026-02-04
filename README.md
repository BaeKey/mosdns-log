# MosDNS Log Viewer

这是一个专为 [mosdns-x](https://github.com/pmkol/mosdns-x) 设计的轻量级日志分析与查看面板。

它通过解析 mosdns 的 `info` 级别日志，提供可视化的仪表盘、查询统计和日志检索功能。

## 功能特性

*   **仪表盘统计**：实时展示最近 24 小时及 7 天的平均响应延迟（支持所有查询类型）。
*   **日志检索**：支持按时间范围、客户端 IP、域名、**查询类型** (A, AAAA, CNAME 等) 进行筛选。
*   **耗时分析**：直观的颜色标记（绿/蓝/橙/红）显示查询耗时等级。
*   **轻量级**：使用 SQLite 存储数据，资源占用极低。
*   **自适应**：美观的 AdGuard Home 风格 UI，适配移动端。

## MosDNS 配置要求

为了使此面板正常工作，您需要在 `mosdns` 中进行以下配置：

1.  **日志级别**：必须设置为 `info`。
2.  **插件建议**：使用 `query_summary` 插件在流水线起始位置来记录查询摘要。

示例配置片段 (`config.yaml`):

```yaml
log:
    file: "./mosdns.log"
    level: info

# ... 流水线配置 ...
  - tag: main_sequence
    type: sequence
    args:
      exec:
        # 记录日志
        - _query_summary # <--- 建议放在最前面
        - ...
```

## 安装与运行

### 1. 下载或编译
您可以从 [Releases](../../releases) 页面下载最新版本，或者手动编译：

```bash
git clone https://github.com/BaeKey/mosdns-log.git
cd mosdns-log
go build -o mosdns-log main.go
```

### 2. 配置
在程序同级目录下修改 `config.yaml`：

```yaml
# mosdns 日志文件位置
log_path: "mosdns.log"
# mosdns 日志文件清理大小（单位MB），超过30M直接清空
log_max_size_mb: 30
# mosdns 日志文件检查时间间隔（单位分钟）
log_check_interval_mins: 60
# 数据库数据保留最近7天的日志数据
db_retention_days: 7
# 数据库储存的日志，检查时间间隔（只保留最近7天的）
db_check_interval_mins: 60
# 程序运行日志文件位置（留空回退到标准输出（stdout）)
app_log_path: ""
# 程序运行日志，输出日志的等级（默认Info）
app_log_level: "info"
# 程序端口
port: "8080"
```

### 3. 运行
```bash
./mosdns-log
# 或者指定配置文件路径
./mosdns-log -c /path/to/config.yaml
```

**注意**：程序每次重启时会**清空**当前的统计数据库，并从日志文件中重新读取数据。

## 访问
打开浏览器访问：`http://localhost:8080` (或您配置的端口)。
