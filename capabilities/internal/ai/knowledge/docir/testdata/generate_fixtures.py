# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Generate original deterministic qualification PDFs; no downloaded documents."""
from pathlib import Path

def pdf(path, pages, text=True):
    objs = [b'<< /Type /Catalog /Pages 2 0 R >>', b'']
    objs.append(b'<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>')
    if not text:
        pixels = b'\x00\xff' * 32
        objs.append(b'<< /Type /XObject /Subtype /Image /Width 8 /Height 8 /ColorSpace /DeviceGray /BitsPerComponent 8 /Length 64 >>\nstream\n'+pixels+b'\nendstream')
    kids = []
    for n in range(pages):
        page_id = len(objs) + 1
        stream_id = page_id + 1
        kids.append(f'{page_id} 0 R')
        image = ' /XObject << /Im0 4 0 R >>' if not text else ''
        objs.append(f'<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >>{image} >> /Contents {stream_id} 0 R >>'.encode())
        stream = b'q 200 0 0 200 50 500 cm /Im0 Do Q\n' if not text else b''
        if text:
            stream = b'BT /F1 12 Tf 50 740 Td (Approval policy) Tj ET\n'
            if n == 3:
                stream += b'BT /F1 12 Tf 50 700 Td (Amount VND) Tj ET\nBT /F1 12 Tf 320 700 Td (Approver) Tj ET\nBT /F1 12 Tf 50 680 Td (>= 10,000,000) Tj ET\nBT /F1 12 Tf 320 680 Td (Director) Tj ET\n'
        objs.append(b'<< /Length ' + str(len(stream)).encode() + b' >>\nstream\n' + stream + b'endstream')
    objs[1] = ('<< /Type /Pages /Kids [' + ' '.join(kids) + f'] /Count {pages} >>').encode()
    out = b'%PDF-1.4\n'; offsets = [0]
    for i, obj in enumerate(objs, 1):
        offsets.append(len(out)); out += f'{i} 0 obj\n'.encode() + obj + b'\nendobj\n'
    xref = len(out); out += f'xref\n0 {len(offsets)}\n0000000000 65535 f \n'.encode()
    for x in offsets[1:]: out += f'{x:010d} 00000 n \n'.encode()
    out += f'trailer\n<< /Size {len(offsets)} /Root 1 0 R >>\nstartxref\n{xref}\n%%EOF\n'.encode()
    Path(__file__).with_name(path).write_bytes(out)

pdf('table.pdf', 4)
pdf('scan.pdf', 1, False)
pdf('too-many-pages.pdf', 201)
