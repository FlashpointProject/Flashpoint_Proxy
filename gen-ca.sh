openssl ecparam -out fpGameServerCA.key -name prime256v1 -genkey
openssl req -new -sha256 -key fpGameServerCA.key -out fpGameServerCA.csr
openssl x509 -req -sha256 -days 36500 -in fpGameServerCA.csr -signkey fpGameServerCA.key -out fpGameServerCA.crt