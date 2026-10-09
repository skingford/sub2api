"""Replay original and production-normalized errors to the unmodified CLI."""
import json
import os
from pathlib import Path
import post_alignment_lab as edge

lab = edge.lab
lab.CASES = [{'name': marker + '-' + side, 'base': 'api-arg', 'system_chars': 600000,
              'error_marker': marker, 'error_side': side,
              'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1',
                      'CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS': '2',
                      'CLAUDE_CODE_MAX_RETRIES': '0'}}
             for marker in ['cf-ray', 'request-id', 'none'] for side in ['upstream', 'downstream']]
lab.CASES.append({'name': 'cf-ray-restored', 'base': 'api-arg', 'system_chars': 600000,
                  'error_marker': 'cf-ray', 'error_side': 'downstream', 'restore_cf_ray': True,
                  'env': {'CLAUDE_CODE_GZIP_REQUEST_BODIES': '1',
                          'CLAUDE_CODE_GZIP_REQUEST_BODY_BLOCKS': '2',
                          'CLAUDE_CODE_MAX_RETRIES': '0'}})
OriginalHandler = lab.Handler


class Handler(OriginalHandler):
    def do_POST(self):
        if self.path.startswith('/v1/messages?'):
            if os.environ.get('CLAUDE_LAB_PLAIN_SUCCESS') == '1' and not self.headers.get('Content-Encoding'):
                return super().do_POST()
            self.record(self.rfile.read(int(self.headers['Content-Length'])))
            lab.audit.STATE['messages'] += 1
            row = json.loads((Path('/error-inputs') / (lab.CURRENT['error_marker'] + '.json')).read_text())
            response = row[lab.CURRENT['error_side']]
            if lab.CURRENT.get('restore_cf_ray'):
                response['headers']['Cf-Ray'] = row['upstream']['headers']['Cf-Ray']
            body = response['body'].encode()
            self.send_response(response['status'])
            for key, values in response['headers'].items():
                if key.lower() in ['content-length', 'connection', 'transfer-encoding']:
                    continue
                for value in values:
                    self.send_header(key, value)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        super().do_POST()


if __name__ == '__main__':
    lab.launch = edge.launch
    lab.Handler = Handler
    lab.main()
