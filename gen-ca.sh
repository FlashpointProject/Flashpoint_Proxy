openssl ecparam -out fpproxy.key -name prime256v1 -genkey
openssl req -new -sha256 -key fpproxy.key -out fpproxy.csr
openssl x509 -req -sha256 -days 36500 -in fpproxy.csr -signkey fpproxy.key -out fpproxy.crt