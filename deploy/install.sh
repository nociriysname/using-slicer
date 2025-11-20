#!/bin/bash
set -e

echo ">>> [1/4] Установка утилит..."
sudo apt-get update
sudo apt-get install -y wget curl qemu-utils cloud-image-utils net-tools

echo ">>> [2/4] Установка Cloud Hypervisor..."
wget https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v40.0/cloud-hypervisor -O /usr/local/bin/cloud-hypervisor
chmod +x /usr/local/bin/cloud-hypervisor

echo ">>> [3/4] Подготовка директорий..."
mkdir -p /var/lib/qudata/images
mkdir -p /var/lib/qudata/instances

echo ">>> [4/4] Скачивание ресурсов..."

if [ ! -f "/var/lib/qudata/images/vmlinux" ]; then
    echo "Downloading Kernel..."
    wget https://github.com/cloud-hypervisor/linux/releases/download/ch-6.2.16/vmlinux-6.2.16 -O /var/lib/qudata/images/vmlinux
fi

if [ ! -f "/var/lib/qudata/images/ubuntu.raw" ]; then
    echo "Downloading Ubuntu Cloud Image..."
    wget https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img -O /tmp/ubuntu.qcow2
    qemu-img convert -f qcow2 -O raw /tmp/ubuntu.qcow2 /var/lib/qudata/images/ubuntu.raw
    rm /tmp/ubuntu.qcow2
fi

echo ">>> Готово! Сервер готов к запуску MicroVM."