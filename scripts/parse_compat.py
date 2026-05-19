#!/usr/bin/env python3
import json, sys

with open('compat-results.json') as f:
    data = json.load(f)

total = 0
failed = 0
for suite in data['Suites']:
    sn = suite['Suite']
    for g in suite['Groups']:
        gn = g.get('Group', '?')
        for op in g.get('Operations', []):
            on = op.get('Operation', '?')
            st = op.get('Status', '?')
            total += 1
            if st != 'pass':
                failed += 1
                err = op.get('Error', '')
                print(f'{sn}/{gn}/{on}: {st} - {str(err)[:120]}')

print(f'\n{failed} failed / {total} total')
