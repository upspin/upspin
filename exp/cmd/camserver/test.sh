#!/bin/sh

echo '
ls camserver@example.com
cp camserver@example.com/frame.jpg .
' | upbox -config=upbox.config
