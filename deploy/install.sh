#!/bin/bash
set -e

echo ">>> [1/4] Установка зависимостей..."
sudo apt-get update
sudo apt-get install -y wget curl qemu-utils cloud-image-utils net-tools docker.io

echo ">>> [2/4] Установка Cloud Hypervisor..."
wget https://github.com/cloud-hypervisor/cloud-hypervisor/releases/download/v40.0/cloud-hypervisor -O /usr/local/bin/cloud-hypervisor
chmod +x /usr/local/bin/cloud-hypervisor

echo ">>> [3/4] Подготовка папок..."
mkdir -p /var/lib/qudata/images
mkdir -p /var/lib/qudata/instances
mkdir -p /var/lib/qudata/cache

echo ">>> [4/4] Скачивание Ядра..."
if [ ! -f "/var/lib/qudata/images/vmlinux" ]; then
    wget https://github.com/cloud-hypervisor/linux/releases/download/ch-6.2.16/vmlinux-6.2.16 -O /var/lib/qudata/images/vmlinux
fi

echo ">>> Готово! Docker установлен (для сборки), Cloud Hypervisor готов (для запуска)."