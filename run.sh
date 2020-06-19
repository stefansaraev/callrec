#!/bin/sh

./terminal 2>/dev/null | ./ambetool -d 2>/dev/null | aplay -t raw -f s16_le -r 8000
