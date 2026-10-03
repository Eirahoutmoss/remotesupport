#!/usr/bin/env bash
# NexDesk sunucu kurulumu (Ubuntu 22.04+): coturn (TURN) + NexDesk signaling.
# Kullanım:  sudo bash sunucu-kur.sh
# İsteğe bağlı:
#   PUBLIC_IP=1.2.3.4 LAN_IP=10.0.0.5   adresleri elle ver
#   KEEP_PLAIN=0                        şifresiz ws://:8091 girişini kapat (tüm istemciler 1.2.0+ olunca)
#   MIN_CLIENT_V=2                      eski (1.1.x) istemcileri reddet
# Örnek: PUBLIC_IP=1.2.3.4 KEEP_PLAIN=0 sudo -E bash sunucu-kur.sh
set -euo pipefail

PUBLIC_IP="${PUBLIC_IP:-$(curl -s4 --max-time 6 https://api.ipify.org || true)}"
LAN_IP="${LAN_IP:-$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src"){print $(i+1); exit}}')}"
SIG_PORT=8091        # şifresiz ws (geçiş dönemi, eski istemciler)
TLS_PORT=8443        # şifreli wss (sertifika pini ile)
TURN_PORT=3478
RELAY_MIN=49160
RELAY_MAX=49200
KEEP_PLAIN="${KEEP_PLAIN:-1}"
MIN_CLIENT_V="${MIN_CLIENT_V:-0}"
LATEST_APP="${LATEST_APP:-1.2.0}"   # istemcilere "en son sürüm" olarak bildirilir
MIN_APP="${MIN_APP:-}"              # bundan eski istemcilere "güncelle" uyarısı (boş=kapalı)
HERE="$(cd "$(dirname "$0")" && pwd)"

[ "$(id -u)" -eq 0 ] || { echo "Lütfen sudo ile çalıştırın: sudo bash $0"; exit 1; }
[ -n "$LAN_IP" ] || { echo "Yerel IP bulunamadı. LAN_IP=... ile verin."; exit 1; }
[ -n "$PUBLIC_IP" ] || { echo "Dış IP öğrenilemedi. PUBLIC_IP=... ile verin."; exit 1; }
[ -f "$HERE/nexdesk-signaling" ] || { echo "nexdesk-signaling dosyası bu betikle aynı klasörde olmalı."; exit 1; }

echo "== Dış IP: $PUBLIC_IP   Yerel IP: $LAN_IP"

# Port çakışması kontrolü (Docker vb. kullanıyor olabilir)
for p in "tcp:$SIG_PORT" "tcp:$TLS_PORT" "udp:$TURN_PORT" "tcp:$TURN_PORT"; do
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

# TURN artık sabit kullanıcı/parola değil, ortak sır (use-auth-secret) kullanır:
# signaling sunucusu her onaylı oturuma 12 saat geçerli ayrı bir kimlik üretir,
# exe'nin içinde hiçbir parola kalmaz. Yeniden kurulumda sır korunur.
mkdir -p /etc/nexdesk && chmod 700 /etc/nexdesk
if [ -f /etc/nexdesk/turn-secret ]; then
  TURN_SECRET="$(cat /etc/nexdesk/turn-secret)"
else
  TURN_SECRET="$(openssl rand -hex 32)"
  ( umask 077; printf '%s' "$TURN_SECRET" > /etc/nexdesk/turn-secret )
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
use-auth-secret
static-auth-secret=$TURN_SECRET
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
# Sır içerdiği için herkese açık olmasın (coturn'ün grubu okuyabilsin).
if getent group turnserver >/dev/null; then chgrp turnserver /etc/turnserver.conf && chmod 640 /etc/turnserver.conf; fi

sed -i 's/^#\?TURNSERVER_ENABLED=.*/TURNSERVER_ENABLED=1/' /etc/default/coturn 2>/dev/null || echo "TURNSERVER_ENABLED=1" > /etc/default/coturn
systemctl enable coturn >/dev/null 2>&1
systemctl restart coturn

echo "== NexDesk signaling kuruluyor"
install -m 0755 "$HERE/nexdesk-signaling" /usr/local/bin/nexdesk-signaling

PLAIN_ADDR="0.0.0.0:$SIG_PORT"
[ "$KEEP_PLAIN" = "0" ] && PLAIN_ADDR="off"
( umask 077; cat > /etc/nexdesk/signaling.env <<EOF
REMOTESUPPORT_SIGNALING_ADDR=$PLAIN_ADDR
REMOTESUPPORT_TLS_ADDR=0.0.0.0:$TLS_PORT
REMOTESUPPORT_TURN_SECRET=$TURN_SECRET
REMOTESUPPORT_TURN_URLS=turn:$PUBLIC_IP:$TURN_PORT?transport=udp,turn:$PUBLIC_IP:$TURN_PORT?transport=tcp,turn:$LAN_IP:$TURN_PORT
REMOTESUPPORT_TURN_TTL=12h
REMOTESUPPORT_MIN_CLIENT_V=$MIN_CLIENT_V
REMOTESUPPORT_LATEST_APP=$LATEST_APP
REMOTESUPPORT_MIN_APP=$MIN_APP
EOF
)

cat > /etc/systemd/system/nexdesk-signaling.service <<EOF
[Unit]
Description=NexDesk signaling
After=network-online.target
Wants=network-online.target

[Service]
EnvironmentFile=/etc/nexdesk/signaling.env
# Öz-imzalı sertifika ilk açılışta burada üretilir ve hep aynı kalır (pin değişmez).
StateDirectory=nexdesk-signaling
Environment=REMOTESUPPORT_TLS_DIR=/var/lib/nexdesk-signaling
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
  for r in "-p tcp --dport $SIG_PORT" "-p tcp --dport $TLS_PORT" "-p udp --dport $TURN_PORT" "-p tcp --dport $TURN_PORT" "-p udp --dport $RELAY_MIN:$RELAY_MAX"; do
    # shellcheck disable=SC2086
    iptables -C INPUT $r -j ACCEPT 2>/dev/null || iptables -I INPUT 1 $r -j ACCEPT
  done
  if command -v netfilter-persistent >/dev/null; then netfilter-persistent save >/dev/null 2>&1 || true; fi
fi

if command -v ufw >/dev/null && ufw status | grep -q "Status: active"; then
  echo "== ufw kuralları ekleniyor"
  ufw allow $SIG_PORT/tcp >/dev/null
  ufw allow $TLS_PORT/tcp >/dev/null
  ufw allow $TURN_PORT/udp >/dev/null
  ufw allow $TURN_PORT/tcp >/dev/null
  ufw allow $RELAY_MIN:$RELAY_MAX/udp >/dev/null
fi

# Sertifika pinini bekle (servis ilk açılışta üretir).
PIN=""
for _ in $(seq 1 20); do
  for f in /var/lib/nexdesk-signaling/pin.txt /var/lib/private/nexdesk-signaling/pin.txt; do
    [ -s "$f" ] && PIN="$(tr -d '\n\r ' < "$f")" && break 2
  done
  sleep 0.5
done
[ -n "$PIN" ] || PIN="(alınamadı: journalctl -u nexdesk-signaling | grep pin)"

ok() { systemctl is-active --quiet "$1" && echo "çalışıyor" || echo "ÇALIŞMIYOR (journalctl -u $1)"; }
WSS_PUB="wss://$PUBLIC_IP:$TLS_PORT/v1/ws#pin=$PIN"
WSS_LAN="wss://$LAN_IP:$TLS_PORT/v1/ws#pin=$PIN"

cat <<EOF

==================== KURULUM TAMAM ====================
coturn            : $(ok coturn)
nexdesk-signaling : $(ok nexdesk-signaling)
şifresiz ws:8091  : $([ "$KEEP_PLAIN" = "0" ] && echo "KAPALI" || echo "açık (geçiş dönemi — herkes 1.2.0'a geçince KEEP_PLAIN=0 ile kapatın)")

--- IT'den istenecek port yönlendirmeleri ($PUBLIC_IP -> $LAN_IP) ---
  TCP  $TLS_PORT   (şifreli signaling)
  TCP  $SIG_PORT   (yalnızca geçiş döneminde)
  UDP  $TURN_PORT  ve  TCP $TURN_PORT
  UDP  $RELAY_MIN-$RELAY_MAX

--- cmd/remotesupport/assets/server-defaults.local.json ---
{
  "signaling_url": "$WSS_PUB, $WSS_LAN",
  "turn_url": "",
  "turn_user": "",
  "turn_pass": ""
}
  (TURN kimliği artık her oturumda sunucudan gelir; exe'de parola tutmayın.)

--- ya da NexDesk > Ayarlar > İnternet Signaling ---
  $WSS_PUB, $WSS_LAN
  TURN alanlarını BOŞ bırakın.
=======================================================
(Bu bilgiler /root/nexdesk-sunucu.txt dosyasına da yazıldı.)
EOF

cat > /root/nexdesk-sunucu.txt <<EOF
Signaling (wss, pinli): $WSS_PUB, $WSS_LAN
Sertifika pini       : $PIN
TURN                 : oturum başına sunucu üretir (sır: /etc/nexdesk/turn-secret)
EOF
chmod 600 /root/nexdesk-sunucu.txt
