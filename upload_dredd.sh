#!/bin/bash
set -e

# Проверка наличия переменной окружения
if [ -z "$REMOTE_IP" ]; then
  echo "Ошибка: переменная окружения DREDD_REMOTE_IP не установлена."
  exit 1
fi

# Сборка бинарника
echo "Сборка Go-программы..."
go build -o dredd cmd/main.go

# Загрузка по SFTP
sftp -i /mnt/d/.ssh-cloud/key artem@"$REMOTE_IP" <<EOF
put dredd /var/tmp/
exit
EOF

echo "Готово. md5sum - $(md5sum dredd)"
