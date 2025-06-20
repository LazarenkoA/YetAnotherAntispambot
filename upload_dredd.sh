#!/bin/bash

# Проверка наличия переменной окружения
if [ -z "$REMOTE_IP" ]; then
  echo "Ошибка: переменная окружения REMOTE_IP не установлена."
  exit 1
fi

# Сборка бинарника
echo "Сборка Go-программы..."
CGO_ENABLED=0 go build -o dredd cmd/main.go

# Загрузка по SFTP
sftp -i /mnt/d/ssh/.ssh-cloud/key artem@"$REMOTE_IP" <<EOF
put dredd /var/tmp/
exit
EOF

echo "Готово. md5sum - $(md5sum dredd)"
