#!/bin/sh

echo '
ls camserver@example.com 
cp camserver@example.com/cam.jpg .
' | upbox -config=upbox.config
