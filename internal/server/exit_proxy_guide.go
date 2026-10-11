package server

import (
	"encoding/json"
	"net"
	"strconv"
	"strings"

	"github.com/Sen62455/PolyFleet/internal/store"
	"go.yaml.in/yaml/v3"
)

const landingSingBoxVersion = "1.12.21"

func buildLandingGuide(proxy store.ExitProxy, password string) landingGuideResponse {
	username := proxy.Username
	if username == "" {
		username = "polyfleet"
	}
	allowFrom := landingAllowFrom(proxy)
	listen := net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port))
	steps := []landingGuideStep{
		{Title: "安装依赖和 sing-box", Where: "落地机 · root 终端", Body: "适用于全新 Debian / Ubuntu（amd64 或 arm64）。每步点击复制，将整段粘贴到终端执行，等该步成功后再继续；不需要自己创建脚本文件。下面会安装依赖、校验官方下载文件并建立专用运行账户。落地机不需要安装 PolyFleet Agent。", Expected: "显示 sing-box version 1.12.21；没有校验失败或下载错误。", Copy: landingPrepareScript()},
		{Title: "写入代理账号和监听配置", Where: "落地机 · root 终端", Filename: "/etc/sing-box/polyfleet-landing.json", Body: "下面的命令会自动写入完整 JSON，无需手动打开编辑器。地址、端口、用户名和密码均来自当前出口配置；代理用户名与 SSH 登录账户无关。已有配置会先备份。复制内容含密码，请妥善保管。", Expected: "显示“代理配置校验通过”；若有错误，修正本步后再继续。", Copy: landingConfigScript(proxy, username, password)},
		{Title: "只放行已绑定的入口节点", Where: "落地机 · root 终端", Filename: "/etc/nftables.d/polyfleet-landing.nft", Body: landingFirewallBody(allowFrom), Expected: "显示解析后的入口 IP 和 active。规则同时限制 TCP 代理端口与 SOCKS5 动态 UDP 端口，并在重启后恢复。", Copy: landingFirewallScript(proxy.Port, allowFrom)},
		{Title: "创建服务并设置开机启动", Where: "落地机 · root 终端", Filename: "/etc/systemd/system/polyfleet-landing.service", Body: "下面会自动创建 systemd 服务文件。服务以 sing-box 专用账户运行，并依赖上一阶段的防火墙。重复执行会重新加载配置、重启服务。", Expected: "最后显示 active 和 enabled，分别表示正在运行、已开机启动。", Copy: landingServiceScript()},
		{Title: "检查监听与服务状态", Where: "落地机 · root 终端", Body: "确认服务和端口正常。SOCKS5 的 UDP 端口在客户端发起 UDP 会话时才出现，空闲时只看到 TCP 监听是正常的。", Expected: "两个服务均为 active；监听列表中出现端口 " + strconv.Itoa(proxy.Port) + "。", Copy: landingScript(`systemctl is-active polyfleet-landing polyfleet-landing-firewall
systemctl is-enabled polyfleet-landing polyfleet-landing-firewall
ss -lntp 'sport = :`+strconv.Itoa(proxy.Port)+`'
nft list table inet polyfleet_landing`, true)},
		{Title: "从入口节点验证出口 IP", Where: "已绑定的入口节点 · 终端", Body: "这一段必须在上方已绑定的入口 VPS 上执行，不是在落地机或自己的电脑上执行。命令会显示入口直出 IP 和通过落地代理的 IP，并使用面板中保存的代理凭据。", Expected: "第二个 IP 应是落地机的公网出口 IP；当前填写的落地地址为 " + proxy.Host + "。出现超时先核对防火墙来源，出现认证错误则检查两边密码。", Copy: landingVerifyScript(proxy, username, password)},
		{Title: "更新订阅并选择链式节点", Where: "本机 · Clash Verge / sing-box 客户端", Body: "更新 PolyFleet 的 Clash 或 sing-box 订阅，选择“入口节点名 · " + proxy.Name + "”。访问公网 IP 查询页面，应显示落地机出口 IP。原来的入口节点仍可直接出网。URI / Base64 订阅不支持链式出口。", Expected: "网站看到的 IP 与上一步的落地出口 IP 一致。"},
		{Title: "以后修改配置与排查问题", Where: "落地机 · root 终端", Body: "改密码：在面板保存后，用 nano 编辑代理 JSON，按 Ctrl+O、回车保存，Ctrl+X 退出；再校验并重启。新增入口或入口换 IP：先更新面板，重新打开本指引并执行第 3 步（也可编辑来源规则文件）。修改面板不会自动改写落地机文件。", Expected: "配置检查通过，服务重启后保持 active。若启动失败，日志会说明原因。若系统中还有 UFW、其他防火墙或云安全组，也需允许入口 IP 访问代理 TCP 端口及动态 UDP 范围，勿向全公网开放。", Copy: landingScript(`# 编辑账号、密码或端口后保存退出；只查看时可直接退出 nano
nano /etc/sing-box/polyfleet-landing.json < /dev/tty
sing-box check -c /etc/sing-box/polyfleet-landing.json
systemctl restart polyfleet-landing
systemctl is-active polyfleet-landing
journalctl -u polyfleet-landing -n 40 --no-pager
# 如编辑过防火墙文件，先检查，再重载：
nft -c -f /etc/nftables.d/polyfleet-landing.nft
systemctl reload polyfleet-landing-firewall`, true)},
	}
	if proxy.ServerSide {
		steps[6].Title = "更新订阅并验证完整链路"
		steps[6].Body = "入口服务器已负责转发。更新订阅后选择“入口节点名 · " + proxy.Name + "”，节点类型仍显示入口协议。Clash、sing-box、URI 和 Base64 均可使用。务必从本机验证出口 IP；节点延迟包含到测速网站的连接和请求耗时，但不是整个网页加载时间。"
		steps[7].Body += " 服务器转发模式还需同步入口机的出口地址、密码和路由；解除绑定或删除面板记录不会自动取消入口机转发。"
		if proxy.Protocol == store.ExitProxyProtocolSOCKS5 {
			snippet, _ := yaml.Marshal(map[string]any{"outbounds": []any{map[string]any{"name": "landing", "type": "socks5", "socks5": map[string]any{"addr": listen, "username": username, "password": password}}}})
			step := landingGuideStep{Title: "配置入口 Hysteria2 转发", Where: "入口机 · /etc/hysteria/config.yaml", Filename: "/etc/hysteria/config.yaml", Body: "仅适用于原生 Hysteria2。先备份该文件，再用 nano 编辑，将下面 outbounds 段合并到顶层，保留原认证、证书和流量统计。已有 outbounds / acl 时需合并路由，不能重复追加；无 ACL 时第一个 outbound 负责所有连接。随后重启 hysteria-server，验证本机出口后再开启面板的“入口服务器转发”。该开关不会自动安装配置。其他入口内核需配置其对应出站。", Expected: "本机经入口访问 IP 查询网站时显示落地机 IP，TCP 和 UDP 均可用。", Copy: string(snippet)}
			steps = append(steps[:6], append([]landingGuideStep{step}, steps[6:]...)...)
		}
	}
	return landingGuideResponse{ProxyID: proxy.ID, Name: proxy.Name, Kind: proxy.Kind, Protocol: proxy.Protocol, Listen: listen, Username: username, AllowFrom: allowFrom, Steps: steps}
}

func landingScript(body string, root bool) string {
	guard := ""
	if root {
		guard = "[ \"$(id -u)\" -eq 0 ] || { echo '请在 root 终端执行'; exit 1; }\n"
	}
	return "bash <<'POLYFLEET_STEP'\nset -euo pipefail\n" + guard + strings.TrimSpace(body) + "\nPOLYFLEET_STEP\n"
}

func landingPrepareScript() string {
	return landingScript(`command -v apt-get >/dev/null || { echo '本教程适用于 Debian / Ubuntu'; exit 1; }
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y curl ca-certificates tar python3 nftables nano
VER='`+landingSingBoxVersion+`'
case "$(uname -m)" in
 x86_64) ARCH=amd64; SHA256=0eb8d90905efaf02828aead3b36b8514630c3454595cf0c4ef366089b10366e7 ;;
 aarch64|arm64) ARCH=arm64; SHA256=b616aa85e72b34a0c0b9030fbcc6bea9ac3b581b7a6bb89bc14e625f248c5d0e ;;
 *) echo '不支持的 CPU 架构'; exit 1 ;;
esac
TMP=$(mktemp -d)
trap 'rm -rf -- "$TMP"' EXIT
curl --fail --location --proto '=https' --tlsv1.2 --retry 2 --max-time 180 \
 -o "$TMP/sing-box.tgz" "https://github.com/SagerNet/sing-box/releases/download/v${VER}/sing-box-${VER}-linux-${ARCH}.tar.gz"
printf '%s  %s\n' "$SHA256" "$TMP/sing-box.tgz" | sha256sum -c -
tar -xzf "$TMP/sing-box.tgz" -C "$TMP" "sing-box-${VER}-linux-${ARCH}/sing-box"
install -d -m 0755 /etc/sing-box /usr/local/bin /var/lib/sing-box /etc/nftables.d
install -m 0755 "$TMP/sing-box-${VER}-linux-${ARCH}/sing-box" /usr/local/bin/sing-box
id sing-box >/dev/null 2>&1 || useradd --system --home /var/lib/sing-box --shell /usr/sbin/nologin sing-box
chown sing-box:sing-box /var/lib/sing-box
sing-box version`, true)
}

func landingSingBoxConfig(proxy store.ExitProxy, username, password string) string {
	config := map[string]any{
		"log":       map[string]any{"level": "warn"},
		"inbounds":  []map[string]any{{"type": "mixed", "tag": "polyfleet-landing", "listen": "::", "listen_port": proxy.Port, "users": []map[string]string{{"username": username, "password": password}}, "set_system_proxy": false}},
		"outbounds": []map[string]any{{"type": "direct", "tag": "direct"}},
	}
	body, _ := json.MarshalIndent(config, "", "  ")
	return string(body) + "\n"
}

func landingConfigScript(proxy store.ExitProxy, username, password string) string {
	return landingScript(`CONFIG=/etc/sing-box/polyfleet-landing.json
if [ -f "$CONFIG" ]; then cp -p "$CONFIG" "$CONFIG.bak.$(date +%Y%m%d-%H%M%S)"; fi
umask 077
cat > "$CONFIG" <<'POLYFLEET_JSON'
`+landingSingBoxConfig(proxy, username, password)+`POLYFLEET_JSON
chown root:sing-box "$CONFIG"
chmod 0640 "$CONFIG"
sing-box check -c "$CONFIG"
echo '代理配置校验通过'`, true)
}

func landingFirewallBody(allowFrom []string) string {
	if len(allowFrom) == 0 {
		return "尚未绑定入口：请先关闭本窗口，在出口编辑页选择至少一台入口并保存，然后重新打开指引。下面的命令会停止执行，不会启动公开代理。"
	}
	return "下面会自动把入口域名解析为 IP，创建独立的 nftables 来源规则和开机加载服务。不会清空其他防火墙规则，也不会修改 SSH 端口。入口必须填写实际出网的公网地址；若入口有 NAT，请先核对来源 IP。"
}

func landingFirewallScript(port int, allowFrom []string) string {
	if len(allowFrom) == 0 {
		return landingScript("echo '请先在 PolyFleet 为该出口绑定入口节点，再重新打开指引'\nexit 1", true)
	}
	hosts, _ := json.Marshal(allowFrom)
	return landingScript(`python3 - <<'POLYFLEET_PY'
import ipaddress, json, os, socket, subprocess, tempfile
from pathlib import Path
hosts = `+string(hosts)+`
port = `+strconv.Itoa(port)+`
addresses = set()
for host in hosts:
    try:
        addresses.add(ipaddress.ip_address(host))
    except ValueError:
        try:
            addresses.update(ipaddress.ip_address(item[4][0]) for item in socket.getaddrinfo(host, None, socket.AF_UNSPEC, socket.SOCK_STREAM))
        except OSError as error:
            raise SystemExit('入口域名解析失败，请检查 DNS：' + host) from error
if not addresses:
    raise SystemExit('未找到入口公网 IP，不应用规则')
low, high = map(int, Path('/proc/sys/net/ipv4/ip_local_port_range').read_text().split())
rules = ['add table inet polyfleet_landing', 'flush table inet polyfleet_landing', 'table inet polyfleet_landing {', ' chain input {', '  type filter hook input priority 0; policy accept;', '  iifname "lo" accept', '  ct state established,related accept']
for address in sorted(addresses, key=lambda ip: (ip.version, int(ip))):
    family = 'ip' if address.version == 4 else 'ip6'
    rules.append(f'  {family} saddr {address} tcp dport {port} counter accept')
    rules.append(f'  {family} saddr {address} udp dport {low}-{high} counter accept')
rules += [f'  tcp dport {port} counter drop', f'  udp dport {low}-{high} counter drop', ' }', '}']
path = Path('/etc/nftables.d/polyfleet-landing.nft')
fd, temporary = tempfile.mkstemp(prefix='.polyfleet-', dir=path.parent)
try:
    with os.fdopen(fd, 'w') as output:
        output.write('\n'.join(rules) + '\n')
    subprocess.run(['nft', '-c', '-f', temporary], check=True)
    if path.exists():
        from datetime import datetime
        import shutil
        shutil.copy2(path, str(path) + '.bak.' + datetime.now().strftime('%Y%m%d-%H%M%S'))
    os.replace(temporary, path)
finally:
    if os.path.exists(temporary): os.unlink(temporary)
print('放行入口 IP：', ', '.join(str(ip) for ip in sorted(addresses, key=str)))
print(f'代理端口 TCP {port}；SOCKS5 动态 UDP {low}-{high}，仅上述来源可访问。')
POLYFLEET_PY
cat > /etc/systemd/system/polyfleet-landing-firewall.service <<'POLYFLEET_UNIT'
[Unit]
Description=PolyFleet landing source restrictions
After=local-fs.target
Before=polyfleet-landing.service

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/usr/sbin/nft -f /etc/nftables.d/polyfleet-landing.nft
ExecReload=/usr/sbin/nft -f /etc/nftables.d/polyfleet-landing.nft

[Install]
WantedBy=multi-user.target
POLYFLEET_UNIT
systemctl daemon-reload
systemctl enable --now polyfleet-landing-firewall
systemctl reload-or-restart polyfleet-landing-firewall
systemctl is-active polyfleet-landing-firewall`, true)
}

func landingServiceScript() string {
	return landingScript(`systemctl is-active --quiet polyfleet-landing-firewall || { echo '请先完成第 3 步防火墙设置'; exit 1; }
sing-box check -c /etc/sing-box/polyfleet-landing.json
cat > /etc/systemd/system/polyfleet-landing.service <<'POLYFLEET_UNIT'
[Unit]
Description=PolyFleet landing exit (sing-box)
After=network-online.target polyfleet-landing-firewall.service
Wants=network-online.target
Requires=polyfleet-landing-firewall.service

[Service]
Type=simple
User=sing-box
Group=sing-box
WorkingDirectory=/var/lib/sing-box
ExecStartPre=/usr/local/bin/sing-box check -c /etc/sing-box/polyfleet-landing.json
ExecStart=/usr/local/bin/sing-box run -c /etc/sing-box/polyfleet-landing.json
Restart=on-failure
RestartSec=3s
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/var/lib/sing-box

[Install]
WantedBy=multi-user.target
POLYFLEET_UNIT
systemctl daemon-reload
systemctl enable polyfleet-landing
systemctl restart polyfleet-landing
systemctl is-active polyfleet-landing
systemctl is-enabled polyfleet-landing`, true)
}

func landingVerifyScript(proxy store.ExitProxy, username, password string) string {
	scheme := "socks5h"
	if proxy.Protocol == store.ExitProxyProtocolHTTP {
		scheme = "http"
	}
	// curl config quoting keeps credentials out of shell expansion and URL parsing.
	quote := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`).Replace(s) + `"`
	}
	return landingScript(`echo '入口直出 IP：'
curl --noproxy '*' -4 -fsS --max-time 12 https://api.ipify.org
echo
echo '通过落地代理的出口 IP：'
curl --config - <<'POLYFLEET_CURL'
proxy = `+quote(scheme+"://"+net.JoinHostPort(proxy.Host, strconv.Itoa(proxy.Port)))+`
proxy-user = `+quote(username+":"+password)+`
noproxy = ""
url = "https://api.ipify.org"
max-time = 20
silent
show-error
fail
POLYFLEET_CURL
echo`, false)
}
