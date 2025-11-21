#!/bin/bash
set -e

echo ">>> [1/6] Установка системных утилит..."
sudo apt-get update
sudo apt-get install -y wget curl qemu-system-x86 genisoimage qemu-utils cloud-image-utils net-tools docker.io

echo ">>> [2/6] Установка Go 1.25..."
sudo rm -rf /usr/local/go
wget https://go.dev/dl/go1.25.0.linux-amd64.tar.gz -O /tmp/go.tar.gz
sudo tar -C /usr/local -xzf /tmp/go.tar.gz
rm /tmp/go.tar.gz

if ! grep -q "/usr/local/go/bin" /etc/profile; then
    echo 'export PATH=$PATH:/usr/local/go/bin' | sudo tee -a /etc/profile
fi
export PATH=$PATH:/usr/local/go/bin

echo ">>> [3/6] Настройка сети (IP Forwarding)..."
sudo sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" | sudo tee /etc/sysctl.d/99-qudata.conf

echo ">>> [4/6] Подготовка папок..."
mkdir -p /var/lib/qudata/images
mkdir -p /var/lib/qudata/instances
mkdir -p /var/lib/qudata/cache

echo ">>> [5/6] Скачивание Ядра (Latest)..."
KERNEL_URL=$(curl -s https://api.github.com/repos/cloud-hypervisor/linux/releases/latest | grep browser_download_url | grep vmlinux | head -n 1 | cut -d '"' -f 4)
echo "Downloading kernel from: $KERNEL_URL"
wget "$KERNEL_URL" -O /var/lib/qudata/images/vmlinux

echo ">>> [6/6] Скачивание базового образа Ubuntu..."
if [ ! -f "/var/lib/qudata/images/ubuntu.raw" ]; then
    wget -q https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img -O /tmp/ubuntu.qcow2
    qemu-img convert -f qcow2 -O raw /tmp/ubuntu.qcow2 /var/lib/qudata/images/ubuntu.raw
    rm /tmp/ubuntu.qcow2
fi

echo ">>> Готово! Go 1.25 установлен, QEMU готов."