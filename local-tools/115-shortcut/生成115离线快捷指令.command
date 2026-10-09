#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"

echo "=== UnpackFlow 115 离线快捷指令生成器 ==="
echo
read -r -p "接口地址（例如 https://example.com/api/115/offline/import）: " api_url
read -r -s -p "快捷指令导入令牌: " api_token
echo

if [[ ! "$api_url" =~ ^https?:// ]]; then
  echo "错误：接口地址必须以 http:// 或 https:// 开头。"
  read -r -p "按回车退出..."
  exit 1
fi
if [[ -z "$api_token" ]]; then
  echo "错误：令牌不能为空。"
  read -r -p "按回车退出..."
  exit 1
fi

unsigned_file="$PWD/115离线导入-未签名.shortcut"
signed_file="$PWD/115离线导入.shortcut"
cp "$PWD/115离线导入.template.shortcut" "$unsigned_file"

/usr/libexec/PlistBuddy -c "Set :WFWorkflowActions:1:WFWorkflowActionParameters:WFURL $api_url" "$unsigned_file"
/usr/libexec/PlistBuddy -c "Set :WFWorkflowActions:1:WFWorkflowActionParameters:WFHTTPHeaders:Authorization Bearer $api_token" "$unsigned_file"
plutil -lint "$unsigned_file"

echo "正在请求 Apple 签名……"
shortcuts sign --mode anyone --input "$unsigned_file" --output "$signed_file"
rm -f "$unsigned_file"

echo
echo "生成成功：$signed_file"
echo "即将打开快捷指令，请在弹出的窗口中选择添加。"
open "$signed_file"
echo
read -r -p "按回车关闭窗口..."
