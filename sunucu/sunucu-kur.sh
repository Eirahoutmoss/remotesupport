#!/usr/bin/env bash
# NexDesk sunucu kurulumu (Ubuntu 22.04+): coturn (TURN) + NexDesk signaling.
# Kullanım:  sudo bash sunucu-kur.sh
# İsteğe bağlı: PUBLIC_IP=1.2.3.4 LAN_IP=10.0.0.5 sudo -E bash sunucu-kur.sh
set -euo pipefail

PUBLIC_IP="${PUBLIC_IP:-$(curl -s4 --max-time 6 https://api.ipify.org || true)}"
LAN_IP="${LAN_IP:-$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')}"
SIG_PORT=8091
TURN_PORT=3478
RELAY_MIN=49160
RELAY_MAX=49200
HERE="$(cd "$(dirname "$0")" && pwd)"

[ "$(id -u)" -eq 0 ] || { echo "Lütfen sudo ile çalıştırın: sudo bash $0"; exit 1; }
[ -n "$LAN_IP" ] || { echo "Yerel IP bulunamadı. LAN_IP=... ile verin."; exit 1; }
[ -n "$PUBLIC_IP" ] || { echo "Dış IP öğrenilemedi. PUBLIC_IP=... ile verin."; exit 1; }
[ -f "$HERE/nexdesk-signaling" ] || { echo "nexdesk-signaling dosyası bu betikle aynı klasörde olmalı."; exit 1; }

echo "== Dış IP: $PUBLIC_IP   Yerel IP: $LAN_IP"

# Port çakışması kontrolü (Docker vb. kullanıyor olabilir)
for p in "tcp:$SIG_PORT" "udp:$TURN_PORT" "tcp:$TURN_PORT"; do
  proto="${p%%:*}"; port="${p##*:}"
  if ss -H -l -n -"${proto:0:1}" "sport = :$port" | grep -q .; then
    owner="$(ss -H -l -n -p -"${proto:0:1}" "sport = :$port" | head -1)"
    case "$owner" in
      *turnserver*|*nexdesk-signal*) ;;  # önceki kurulum, sorun değil
      *) echo "HATA: $proto/$port kullanımda: $owner"; exit 1 ;;
    esac
  fi
done

echo "== coturn kuruluyor"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq coturn openssl >/dev/null

TURN_USER="nexdesk"
if [ -f /etc/turnserver.conf ] && grep -q '^user=nexdesk:' /etc/turnserver.conf; then
  TURN_PASS="$(grep '^user=nexdesk:' /etc/turnserver.conf | head -1 | cut -d: -f2)"   # yeniden kurulumda parolayı koru
else
  TURN_PASS="$(openssl rand -hex 16)"
fi
[ -f /etc/turnserver.conf ] && cp /etc/turnserver.conf "/etc/turnserver.conf.yedek-$(date +%Y%m%d%H%M%S)"

# Kurum içi alt ağ (ör. 10.2.0.0/16): aynı ağdaki teknisyenlere aktarım için izinli.
LAN_A="$(echo "$LAN_IP" | cut -d. -f1)"; LAN_B="$(echo "$LAN_IP" | cut -d. -f2)"

cat > /etc/turnserver.conf <<EOF
# NexDesk TURN — sunucu-kur.sh tarafından oluşturuldu
listening-port=$TURN_PORT
listening-ip=$LAN_IP
relay-ip=$LAN_IP
external-ip=$PUBLIC_IP/$LAN_IP
min-port=$RELAY_MIN
max-port=$RELAY_MAX
realm=nexdesk
fingerprint
lt-cred-mech
user=$TURN_USER:$TURN_PASS
no-cli
no-tls
no-dtls
no-multicast-peers
# Güvenlik: TURN'ün iç ağa köprü olmasını engelle (kurum alt ağı hariç)
denied-peer-ip=0.0.0.0-0.255.255.255
denied-peer-ip=10.0.0.0-10.255.255.255
denied-peer-ip=100.64.0.0-100.127.255.255
denied-peer-ip=127.0.0.0-127.255.255.255
denied-peer-ip=169.254.0.0-169.254.255.255
denied-peer-ip=172.16.0.0-172.31.255.255
denied-peer-ip=192.168.0.0-192.168.255.255
allowed-peer-ip=$LAN_A.$LAN_B.0.0-$LAN_A.$LAN_B.255.255
allowed-peer-ip=$PUBLIC_IP
syslog
simple-log
EOF

sed -i 's/^#\?TURNSERVER_ENABLED=.*/TURNSERVER_ENABLED=1/' /etc/default/coturn 2>/dev/null || echo "TURNSERVER_ENABLED=1" > /etc/default/coturn
systemctl enable coturn >/dev/null 2>&1
systemctl restart coturn

echo "== NexDesk signaling kuruluyor"
install -m 0755 "$HERE/nexdesk-signaling" /usr/local/bin/nexdesk-signaling
cat > /etc/systemd/system/nexdesk-signaling.service <<EOF
[Unit]
Description=NexDesk signaling
After=network-online.target
Wants=network-online.target

[Service]
Environment=REMOTESUPPORT_SIGNALING_ADDR=0.0.0.0:$SIG_PORT
ExecStart=/usr/local/bin/nexdesk-signaling
Restart=always
RestartSec=3
DynamicUser=yes
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable nexdesk-signaling >/dev/null 2>&1
systemctl restart nexdesk-signaling

# Oracle Cloud vb. imajlar: ufw kapalı ama iptables INPUT zinciri 22 dışındakileri REJECT eder.
if iptables -S INPUT 2>/dev/null | grep -q -- "-j REJECT"; then
  echo "== iptables kuralları ekleniyor"
  for r in "-p tcp --dport $SIG_PORT" "-p udp --dport $TURN_PORT" "-p tcp --dport $TURN_PORT" "-p udp --dport $RELAY_MIN:$RELAY_MAX"; do
    # shellcheck disable=SC2086
    iptables -C INPUT $r -j ACCEPT 2>/dev/null || iptables -I INPUT 1 $r -j ACCEPT
  done
  if command -v netfilter-persistent >/dev/null; then netfilter-persistent save >/dev/null 2>&1 || true; fi
fi

if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  echo "== ufw kuralları ekleniyor"
  ufw allow $SIG_PORT/tcp >/dev/null
  ufw allow $TURN_PORT/udp >/dev/null
  ufw allow $TURN_PORT/tcp >/dev/null
  ufw allow $RELAY_MIN:$RELAY_MAX/udp >/dev/null
fi

sleep 2
ok() { systemctl is-active --quiet "$1" && echo "çalışıyor" || echo "ÇALIŞMIYOR (journalctl -u $1)"; }

cat <<EOF

==================== KURULUM TAMAM ====================
coturn            : $(ok coturn)
nexdesk-signaling : $(ok nexdesk-signaling)

--- IT'den istenecek port yönlendirmeleri ($PUBLIC_IP -> $LAN_IP) ---
  TCP  $SIG_PORT
  UDP  $TURN_PORT  ve  TCP $TURN_PORT
  UDP  $RELAY_MIN-$RELAY_MAX

--- NexDesk > Ayarlar ---
  İnternet Signaling : ws://$PUBLIC_IP:$SIG_PORT/v1/ws, ws://$LAN_IP:$SIG_PORT/v1/ws
  TURN Adres         : turn:$PUBLIC_IP:$TURN_PORT, turn:$LAN_IP:$TURN_PORT
  TURN Kullanıcı     : $TURN_USER
  TURN Parola        : $TURN_PASS
=======================================================
(Bu bilgiler /root/nexdesk-sunucu.txt dosyasına da yazıldı.)
EOF

cat > /root/nexdesk-sunucu.txt <<EOF
Signaling: ws://$PUBLIC_IP:$SIG_PORT/v1/ws, ws://$LAN_IP:$SIG_PORT/v1/ws
TURN     : turn:$PUBLIC_IP:$TURN_PORT, turn:$LAN_IP:$TURN_PORT
Kullanıcı: $TURN_USER
Parola   : $TURN_PASS
EOF
chmod 600 /root/nexdesk-sunucu.txt
