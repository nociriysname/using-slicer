#!/bin/bash
set -e

echo ">>> [1/4] Установка утилит..."
sudo apt-get update
sudo apt-get install -y wget curl qemu-utils cloud-image-utils net-tools docker.io

echo ">>> [2/4] Установка Cloud Hypervisor..."
if [ ! -f "/usr/local/bin/cloud-hypervisor" ]; then
    wget https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v40.0/cloud-hypervisor -O /usr/local/bin/cloud-hypervisor
    chmod +x /usr/local/bin/cloud-hypervisor
fi

echo ">>> [3/4] Подготовка папок..."
mkdir -p /var/lib/qudata/images
mkdir -p /var/lib/qudata/instances
mkdir -p /var/lib/qudata/cache

echo ">>> [4/4] Скачивание ресурсов..."

if [ ! -f "/var/lib/qudata/images/vmlinux" ]; then
    echo "Downloading Kernel (6.8.0)..."
    wget https://github.com/cloud-hypervisor/linux/releases/download/ch-6.8.0/vmlinux-6.8.0 -O /var/lib/qudata/images/vmlinux
fi

if [ ! -f "/var/lib/qudata/images/ubuntu.raw" ]; then
    echo "Downloading Ubuntu Cloud Image..."
    wget -q https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img -O /tmp/ubuntu.qcow2
    qemu-img convert -f qcow2 -O raw /tmp/ubuntu.qcow2 /var/lib/qudata/images/ubuntu.raw
    rm /tmp/ubuntu.qcow2
fi

echo ">>> Готово! Окружение настроено."