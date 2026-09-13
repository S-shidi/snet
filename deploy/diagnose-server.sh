#!/bin/bash
# SNET 服务器诊断脚本
# 在服务器上运行此脚本以收集诊断信息

set -e

echo "=========================================="
echo "SNET 服务器诊断报告"
echo "时间: $(date)"
echo "=========================================="
echo ""

echo "【1. 系统信息】"
echo "主机名: $(hostname)"
echo "系统: $(uname -a)"
echo "内核: $(uname -r)"
echo ""

echo "【2. CPU 和内存】"
echo "CPU 核心数: $(nproc)"
echo "CPU 使用率:"
top -bn1 | head -5
echo ""
echo "内存使用:"
free -h
echo ""

echo "【3. 磁盘使用】"
df -h | grep -E "Filesystem|/$|/var|/tmp"
echo ""

echo "【4. 网络状态】"
echo "监听端口:"
netstat -tulpn 2>/dev/null | grep -E "8090|51820|8091" || ss -tulpn | grep -E "8090|51820|8091"
echo ""

echo "【5. SNET 进程】"
ps aux | grep -E "snet|wireguard" | grep -v grep
echo ""

echo "【6. 数据库检查】"
# 查找数据库文件
DB_FILE=$(find /var/lib/snet /opt/snet /root -name "*.db" -o -name "snet.db" 2>/dev/null | head -1)

if [ -n "$DB_FILE" ]; then
    echo "数据库文件: $DB_FILE"
    echo "数据库大小: $(ls -lh "$DB_FILE" | awk '{print $5}')"
    echo ""
    echo "数据库表:"
    sqlite3 "$DB_FILE" ".tables" 2>/dev/null || echo "无法访问数据库"
    echo ""
    echo "数据库索引:"
    sqlite3 "$DB_FILE" "SELECT name FROM sqlite_master WHERE type='index';" 2>/dev/null || echo "无法查询索引"
    echo ""
    echo "节点数量:"
    sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM nodes;" 2>/dev/null || echo "无法查询"
    echo ""
    echo "端点数量:"
    sqlite3 "$DB_FILE" "SELECT COUNT(*) FROM endpoints;" 2>/dev/null || echo "无法查询"
else
    echo "未找到数据库文件"
fi
echo ""

echo "【7. 日志分析】"
LOG_FILE=$(find /var/log -name "*snet*" -type f 2>/dev/null | head -1)
if [ -n "$LOG_FILE" ]; then
    echo "日志文件: $LOG_FILE"
    echo "最近错误:"
    tail -100 "$LOG_FILE" | grep -E "ERROR|WARN|error|warning" | tail -10
    echo ""
    echo "慢请求:"
    tail -100 "$LOG_FILE" | grep -i "slow\|timeout\|deadline exceeded" | tail -10
else
    echo "未找到日志文件"
fi
echo ""

echo "【8. API 性能测试】"
echo "测试 /healthz:"
curl -w "响应时间: %{time_total}s, HTTP: %{http_code}\n" -o /dev/null -s http://localhost:8090/healthz 2>&1 || echo "无法连接到 API"
echo ""

echo "【9. 网络延迟】"
echo "Ping 8.8.8.8:"
ping -c 3 8.8.8.8 | tail -2
echo ""

echo "【10. 配置文件】"
CONF_FILE=$(find /etc /opt /root -name "*snet*" -type f 2>/dev/null | grep -E "\.conf$|\.yaml$|\.yml$|\.json$" | head -1)
if [ -n "$CONF_FILE" ]; then
    echo "配置文件: $CONF_FILE"
    cat "$CONF_FILE"
else
    echo "未找到配置文件"
fi
echo ""

echo "=========================================="
echo "诊断完成"
echo "=========================================="
echo ""
echo "建议优化:"
echo "1. 检查数据库索引是否存在"
echo "2. 查看是否有慢查询日志"
echo "3. 监控服务器资源使用"
echo "4. 考虑增加缓存层"