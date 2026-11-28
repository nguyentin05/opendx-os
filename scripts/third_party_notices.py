#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
"""Write the license and NOTICE texts of third-party code shipped in builds.

Covers what the project's binaries and bundles contain: every Go module linked
into a command of operator/ or capabilities/ (and the Go standard library), and
the npm packages bundled into the Workspace UI, and Python AI runtime wheels.
Needs the Go module cache, workspace-ui/node_modules, and the locked AI venv.

    python3 scripts/third_party_notices.py            # rewrite the file
    python3 scripts/third_party_notices.py --check    # fail when it is stale
"""

from __future__ import annotations

import hashlib
import importlib.metadata
import io
import json
import os
import pathlib
import platform
import re
import subprocess
import sys
import sysconfig
import tarfile
import tomllib
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parent.parent
OUT = ROOT / "THIRD_PARTY_LICENSES.txt"
LEGAL = re.compile(r"(?i)^(licen[cs]e|copying|notice)([.-].*)?$")
# Supplemental texts are downloaded into this cache, outside the repository and
# Git, keyed by their SHA-256. Nothing license-related is committed here: the
# generated THIRD_PARTY_LICENSES.txt is the only copy a reader needs.
CACHE = pathlib.Path(os.environ.get("XDG_CACHE_HOME", pathlib.Path.home() / ".cache")) / "opendx-os/licenses"

# Per-distribution review of the pinned AI runtime. `source` is the sdist the
# lockfile pins, or the upstream tag/commit the wheel was built from. Each
# `files` entry is a text the wheel omits: a pinned upstream URL plus the
# SHA-256 of the exact bytes reproduced in THIRD_PARTY_LICENSES.txt.
REVIEWED = {'accelerate 1.16.0': {'license': 'Apache-2.0',
                       'source': 'https://files.pythonhosted.org/packages/62/dd/14c28a3918ae4d3e40d87e2edcb8737731e52c57f28f0980bb780f50dad6/accelerate-1.16.0.tar.gz#sha256=43c446b9affb145220946a63fe32e14e968fcfca645d25b5f1f3fdbc375657ae'},
 'aiohappyeyeballs 2.7.1': {'license': 'PSF-2.0 and historical Python terms',
                            'notes': ['REVIEW NOTE (optional): The PSF and historical Python '
                                      'license texts are retained. Record their scope and OSI '
                                      'classification separately.'],
                            'source': 'https://files.pythonhosted.org/packages/ce/f4/eec0465c2f67b2664688d0240b3212d5196fd89e741df67ddb81f8d35658/aiohappyeyeballs-2.7.1.tar.gz#sha256=065665c041c42a5938ed220bdcd7230f22527fbec085e1853d2402c8a3615d9d'},
 'aiosignal 1.4.0': {'license': 'Apache-2.0',
                     'source': 'https://files.pythonhosted.org/packages/61/62/06741b579156360248d1ec624842ad0edf697050bbaf7c3e46394e106ad1/aiosignal-1.4.0.tar.gz#sha256=f47eecd9468083c2029cc99945502cb7708b082c232f9aca65da147157b251c7'},
 'amqp 5.4.2': {'license': 'BSD-3-Clause',
                'source': 'https://files.pythonhosted.org/packages/4b/46/64c69cdd9705a5b312920cf76c3cb7c2c4718ffeafbfbcf87a842890765e/amqp-5.4.2.tar.gz#sha256=9362660c4a31150cf18e78cae2ce8aa1e929fc6c6739e69f6dd5dbea9c3edc82'},
 'beautifulsoup4 4.15.0': {'license': 'MIT',
                           'source': 'https://files.pythonhosted.org/packages/43/65/318323f98dbee45d42dff61d8f047181bc6f2268a9068cfad035a46be5af/beautifulsoup4-4.15.0.tar.gz#sha256=288e3ca7d54b06f2ac191970bc275c1939cb46d450b255bf6718b04aa37ab4f7'},
 'billiard 4.3.2': {'license': 'BSD-3-Clause',
                    'source': 'https://files.pythonhosted.org/packages/96/ba/4ee8c6d14b3e9b5ce89b5b070f1352e6cadefde3f6bc36afcfee5e7158a2/billiard-4.3.2.tar.gz#sha256=9eb83c99b1cf7acb1fed86c1dfd477157000a1a2c0108005c1e5837b55d2acba'},
 'boto3 1.44.12': {'license': 'Apache-2.0',
                   'source': 'https://files.pythonhosted.org/packages/00/96/e538b78f081041395c531cff509e505be16f1003fb4e4e87b33a2573dbec/boto3-1.44.12.tar.gz#sha256=7459cd7a476eef9a5a8044f5cd6b2dfa47cef0fcb74aefa951e154c5102b1199'},
 'botocore 1.44.12': {'license': 'Apache-2.0 for botocore',
                      'notes': ['The matching upstream NOTICE also covers vendored MIT and '
                                'Apache-2.0 code.'],
                      'source': 'https://files.pythonhosted.org/packages/c2/c1/8a229eab793d4ad48bdc5a04d6a2e6edd861c45c22e90d45c0337fcbf83a/botocore-1.44.12.tar.gz#sha256=5806bb66bf167fa3172f4ddf78b088db9b22653702537b19969d02d2c3301ee5'},
 'celery 5.7.0': {'license': 'BSD-3-Clause',
                  'source': 'https://files.pythonhosted.org/packages/ef/f7/166bfd22cbd0dd4f77715ddd207881c1332cd1f304d79b1a123aff7cdf10/celery-5.7.0.tar.gz#sha256=6a5dfcf6662287204f009a02002b9e66c83d4105e47ebabdc7079108772fc6f0'},
 'certifi 2026.11.20': {'license': 'MPL-2.0',
                        'source': 'https://files.pythonhosted.org/packages/51/33/4cd27639502f010084bcef7a2036391dd637439901341ce1a530f36f6e51/certifi-2026.11.20.tar.gz#sha256=af1cb240a8133f105aa52684a3a3258339d7f2fd0bd4e2812698ff5cf5588c4e'},
 'charset-normalizer 3.5.3': {'license': 'MIT',
                              'source': 'https://files.pythonhosted.org/packages/c2/c3/cae6d3bdefd96a941ffc247c714ac4babc488a69012e11c058f6c17c6c47/charset_normalizer-3.5.3.tar.gz#sha256=a250888753faa289e2997962fbb17c4960cbdb58575533ca6fceee68c224bdff'},
 'click-didyoumean 0.3.1': {'license': 'MIT',
                            'source': 'https://files.pythonhosted.org/packages/30/ce/217289b77c590ea1e7c24242d9ddd6e249e52c795ff10fac2c50062c48cb/click_didyoumean-0.3.1.tar.gz#sha256=4f82fdff0dbe64ef8ab2279bd6aa3f6a99c3b28c05aa09cbfc07c9d7fbb5a463'},
 'click-plugins 1.1.1.2': {'license': 'BSD-3-Clause',
                           'source': 'https://files.pythonhosted.org/packages/c3/a4/34847b59150da33690a36da3681d6bbc2ec14ee9a846bc30a6746e5984e4/click_plugins-1.1.1.2.tar.gz#sha256=d7af3984a99d243c131aa1a828331e7630f4a88a9741fd05c927b204bcf92261'},
 'click-repl 0.4.1': {'license': 'MIT',
                      'source': 'https://files.pythonhosted.org/packages/28/50/bea78619ff1fc0fbd61882f64a1302a8abb2ea0b3db92907042d0e362df2/click_repl-0.4.1.tar.gz#sha256=c32a1cf6f95e5bd6e92076f81ce24eafd33f2f0ffb0135887e335b8e446d1c0b'},
 'colorlog 6.12.0': {'license': 'MIT',
                     'source': 'https://files.pythonhosted.org/packages/8c/55/ba79756cb90c8d69d599d57785398ac87bba7b19c80e87f4e8a562197c93/colorlog-6.12.0.tar.gz#sha256=2a7924c1dadf18b22a0eb8b06d1c7b01d5341707ec1641eb6fcc4fde0c3e8e5f'},
 'defusedxml 0.7.1': {'license': 'PSF-2.0',
                      'notes': ['REVIEW NOTE (optional): The PSF license text is retained. Clarify '
                                'the OSI classification of the standalone PSF-2.0 text.'],
                      'source': 'https://files.pythonhosted.org/packages/0f/d5/c66da9b79e5bdb124974bfe172b4daf3c984ebd9c2a06e2b8a4dc7331c72/defusedxml-0.7.1.tar.gz#sha256=1bb3032db185915b62d7c6209c5a8792be6a32ab2fedacc84e01b52c51aa3e69'},
 'dill 0.4.1': {'license': 'BSD-3-Clause',
                'source': 'https://files.pythonhosted.org/packages/81/e1/56027a71e31b02ddc53c7d65b01e68edf64dea2932122fe7746a516f75d5/dill-0.4.1.tar.gz#sha256=423092df4182177d4d8ba8290c8a5b640c66ab35ec7da59ccfa00f6fa3eea5fa'},
 'distro 1.9.0': {'license': 'Apache-2.0',
                  'source': 'https://files.pythonhosted.org/packages/fc/f8/98eea607f65de6527f8a2e8885fc8015d3e6f5775df186e443e0964a11c3/distro-1.9.0.tar.gz#sha256=2fa77c6fd8940f116ee1d6b94a2f90b13b5ea8d019b98bc8bafdcabcdd9bdbed'},
 'docling 2.154.0': {'files': [{'name': 'docling-2.154.0.LICENSE',
                                'sha256': 'edb765d1e3e60d0c7a7d5fba5f2bb2c3c29f7465cecb5c22cf1185c884954e4c',
                                'url': 'https://raw.githubusercontent.com/docling-project/docling/6b5ab0d12ace4e222cb454051492a1ae5db8aa99/LICENSE'}],
                     'license': 'MIT',
                     'source': 'https://raw.githubusercontent.com/docling-project/docling/6b5ab0d12ace4e222cb454051492a1ae5db8aa99/LICENSE'},
 'docstring-parser 0.18.0': {'license': 'MIT',
                             'source': 'https://files.pythonhosted.org/packages/e0/4d/f332313098c1de1b2d2ff91cf2674415cc7cddab2ca1b01ae29774bd5fdf/docstring_parser-0.18.0.tar.gz#sha256=292510982205c12b1248696f44959db3cdd1740237a968ea1e2e7a900eeb2015'},
 'et-xmlfile 2.0.0': {'license': 'MIT with Python standard-library attribution',
                      'notes': ['REVIEW NOTE (optional): MIT and LICENCE.python are retained. '
                                'Identify the CPython revision and historical terms for the copied '
                                'code if available.'],
                      'source': 'https://files.pythonhosted.org/packages/d3/38/af70d7ab1ae9d4da450eeec1fa3918940a5fafb9055e934af8d6eb0c2313/et_xmlfile-2.0.0.tar.gz#sha256=dab3f4764309081ce75662649be815c4c9081e88f0837825f90fd28317d4da54'},
 'faker 40.63.0': {'license': 'MIT',
                   'source': 'https://files.pythonhosted.org/packages/48/e5/3121da6a4e8721773d787c654a7b3e6901eb7df455ffc6391708794de9c1/faker-40.63.0.tar.gz#sha256=6c5a09bfb17b3a5fa29f349a2ea86ec5af959df95ec71adc831268adfccc9d62'},
 'fastuuid 0.14.0': {'license': 'BSD-3-Clause for upstream source',
                     'notes': ['REVIEW NOTE (optional): Compare the Rust runtime dependencies with '
                               'the retained notices. No specific missing attribution was '
                               'identified.'],
                     'source': 'https://files.pythonhosted.org/packages/c3/7d/d9daedf0f2ebcacd20d599928f8913e9d2aea1d56d2d355a93bfa2b611d7/fastuuid-0.14.0.tar.gz#sha256=178947fc2f995b38497a74172adee64fdeb8b7ec18f2a5934d037641ba265d26'},
 'filetype 1.2.0': {'license': 'MIT',
                    'source': 'https://files.pythonhosted.org/packages/bb/29/745f7d30d47fe0f251d3ad3dc2978a23141917661998763bebb6da007eb1/filetype-1.2.0.tar.gz#sha256=66b56cd6474bf41d8c54660347d37afcc3f7d1970648de365c102ef77548aadb'},
 'frozenlist 1.8.0': {'license': 'Apache-2.0',
                      'source': 'https://files.pythonhosted.org/packages/2d/f5/c831fac6cc817d26fd54c7eaccd04ef7e0288806943f7cc5bbf69f3ac1f0/frozenlist-1.8.0.tar.gz#sha256=3ede829ed8d842f6cd48fc7081d7a41001a56f1f38603f9d49bf3020d59a31ad'},
 'h11 0.16.0': {'license': 'MIT',
                'source': 'https://files.pythonhosted.org/packages/01/ee/02a2c011bdab74c6fb3c75474d40b3052059d95df7e73351460c8588d963/h11-0.16.0.tar.gz#sha256=4e35b956cf45792e4caa5885e69fba00bdbc6ffafbfa020300e549b208ee5ff1'},
 'huggingface-hub 2.5.0': {'license': 'Apache-2.0',
                           'source': 'https://files.pythonhosted.org/packages/75/2a/b00ccb0cb53e46f2d1fef791bab4daefcdea5ec8deae478efee52a67b988/huggingface_hub-2.5.0.tar.gz#sha256=6ad8a9ea6f7e7b193f16cd94616cd2291fcde073bd20c28a73b567bfcc2ed94d'},
 'hyperframe 6.1.0': {'license': 'MIT',
                      'source': 'https://files.pythonhosted.org/packages/02/e7/94f8232d4a74cc99514c13a9f995811485a6903d48e5d952771ef6322e30/hyperframe-6.1.0.tar.gz#sha256=f630908a00854a7adeabd6382b43923a4c4cd4b821fcb527e6ab9e15382a3b08'},
 'jinja2 3.1.6': {'license': 'BSD-3-Clause',
                  'source': 'https://files.pythonhosted.org/packages/df/bf/f7da0350254c0ed7c72f3e33cef02e048281fec7ecec5f032d4aac52226b/jinja2-3.1.6.tar.gz#sha256=0137fb05990d35f1275a587e9aee6d56da821fc83491a0fb838183be43f66d6d'},
 'jmespath 1.1.0': {'license': 'MIT',
                    'source': 'https://files.pythonhosted.org/packages/d3/59/322338183ecda247fb5d1763a6cbe46eff7222eaeebafd9fa65d4bf5cb11/jmespath-1.1.0.tar.gz#sha256=472c87d80f36026ae83c6ddd0f1d05d4e510134ed462851fd5f754c8c3cbb88d'},
 'jsonref 1.1.0': {'license': 'MIT',
                   'source': 'https://files.pythonhosted.org/packages/aa/0d/c1f3277e90ccdb50d33ed5ba1ec5b3f0a242ed8c1b1a85d3afeb68464dca/jsonref-1.1.0.tar.gz#sha256=32fe8e1d85af0fdefbebce950af85590b22b60f9e95443176adbde4e1ecea552'},
 'kombu 5.7.0': {'license': 'BSD-3-Clause',
                 'source': 'https://files.pythonhosted.org/packages/57/31/3e632ebce43c684dcc6eead4660b33307e6153be87e33c14e1ed9875deff/kombu-5.7.0.tar.gz#sha256=31e62fdfd2381caa3422f8e02b84a491d9a2180ddda74507b57f4cb1e1c2c2c9'},
 'langchain-core 1.8.5': {'license': 'MIT',
                          'source': 'https://files.pythonhosted.org/packages/8d/6c/467e7f073aa29d8b9338f699545b8618ec6fd221ec838711dea902455246/langchain_core-1.8.5.tar.gz#sha256=f0391367c1058ccec34dc248238415d8fccfa42db1727b937357888ff1da75cb'},
 'langchain-protocol 0.0.22': {'license': 'MIT',
                               'source': 'https://files.pythonhosted.org/packages/a2/5c/94ffa0c0b9445ac284bf7c0aebf2fa3a32bdf27c20aaf2f061746375b670/langchain_protocol-0.0.22.tar.gz#sha256=61267bc9f478c9df35ccdafd9fdf556b2698435232d59def54b3809bb02585bb'},
 'langcodes 3.5.1': {'license': 'MIT',
                     'source': 'https://files.pythonhosted.org/packages/a9/75/f9edc5d72945019312f359e69ded9f82392a81d49c5051ed3209b100c0d2/langcodes-3.5.1.tar.gz#sha256=40bff315e01b01d11c2ae3928dd4f5cbd74dd38f9bd912c12b9a3606c143f731'},
 'latex2mathml 3.81.1': {'files': [{'name': 'latex2mathml-3.81.1.LICENSE',
                                    'sha256': '4ac1e1da07f6b343d54191c5e1716840534e08591b3f9eaec90d6800cdc47543',
                                    'url': 'https://raw.githubusercontent.com/roniemartinez/latex2mathml/fb724a91658cf41d2fad0192da0845b05446f208/LICENSE'}],
                         'license': 'MIT',
                         'source': 'https://raw.githubusercontent.com/roniemartinez/latex2mathml/fb724a91658cf41d2fad0192da0845b05446f208/LICENSE'},
 'lazy-imports 1.2.0': {'license': 'Apache-2.0',
                        'source': 'https://files.pythonhosted.org/packages/25/67/04432aae0c1e2729bff14e1841f4a3fb63a9e354318e66622251487760c3/lazy_imports-1.2.0.tar.gz#sha256=3c546b3c1e7c4bf62a07f897f6179d9feda6118e71ef6ecc47a339cab3d2e2d9'},
 'lxml 6.1.3': {'files': [{'name': 'lxml-6.1.3-libiconv-1.18.LGPL-2.1',
                           'sha256': '20e50fe7aae3e56378ebf0417d9de904f55a0e61e4df315333e632a4d3555d95',
                           'url': 'https://www.gnu.org/licenses/old-licenses/lgpl-2.1.txt'}],
                'license': 'BSD-3-Clause for lxml',
                'notes': ['GNU libiconv 1.18 is embedded in etree.so (_libiconv_version = 0x0112). '
                          'Upstream source: https://ftp.gnu.org/gnu/libiconv/libiconv-1.18.tar.gz',
                          'LGPL-2.1 text: https://www.gnu.org/licenses/old-licenses/lgpl-2.1.txt',
                          'Corresponding source: GNU libiconv 1.18, '
                          'https://ftp.gnu.org/gnu/libiconv/libiconv-1.18.tar.gz (sha256 '
                          '3b08f5f4f9b4eb82f151a7040bfd6fe6c6fb922efe4b1659c66ea933276965e8), '
                          'built into etree.so by the lxml wheel recipe with no distribution '
                          'patches.',
                          'Build recipe: '
                          'https://github.com/lxml/lxml/blob/master/tools/manylinux.sh in the lxml '
                          '6.1.3 sdist above; it builds libiconv with --disable-shared and links '
                          'it into etree.so.',
                          'libiconv is linked statically into etree.so, not shipped as a separate '
                          'shared object, so a recipient replaces it by rebuilding etree.so from '
                          'these sources (LGPL-2.1 section 6(a)).'],
                'source': 'https://files.pythonhosted.org/packages/23/ad/28ecd7cb894d172f3c9c80a075eeeb2017ac62e3632cee05a5f9493547eb/lxml-6.1.3.tar.gz#sha256=45222d94ddd511536f3b2f7d9deae3b2339b4ce0f075f1ca25703b07cad9dd21'},
 'markdown-it-py 4.3.0': {'license': 'MIT',
                          'source': 'https://files.pythonhosted.org/packages/16/fc/cee2c45553c9a9bf77d8b0c391560c556d7ed756b1f0bb35eb5d48db09ea/markdown_it_py-4.3.0.tar.gz#sha256=91a58fa5b72c71a40cf7177250fd767480d7a2bb3ac426ccfb76fb90305fdaaa'},
 'marko 2.2.5': {'license': 'MIT',
                 'source': 'https://files.pythonhosted.org/packages/65/55/7ba85e14361632d734998b6a7f676de73d986a2ebb15b1008c794bf70ccf/marko-2.2.5.tar.gz#sha256=3a5bac3a2e3ccb53cd97dcd29e422c4f701c29ec7c61be4c0d827321ef057553'},
 'mdurl 0.1.2': {'license': 'MIT',
                 'source': 'https://files.pythonhosted.org/packages/d6/54/cfe61301667036ec958cb99bd3efefba235e65cdeb9c84d24a8293ba1d90/mdurl-0.1.2.tar.gz#sha256=bb413d29f5eea38f31dd4754dd7377d4465116fb207585f97bf925588687c1ba'},
 'mpire 2.10.2': {'license': 'MIT',
                  'source': 'https://files.pythonhosted.org/packages/3a/93/80ac75c20ce54c785648b4ed363c88f148bf22637e10c9863db4fbe73e74/mpire-2.10.2.tar.gz#sha256=f66a321e93fadff34585a4bfa05e95bd946cf714b442f51c529038eb45773d97'},
 'mpmath 1.3.0': {'license': 'BSD-3-Clause',
                  'source': 'https://files.pythonhosted.org/packages/e0/47/dd32fa426cc72114383ac549964eecb20ecfd886d1e5ccf5340b55b02f57/mpmath-1.3.0.tar.gz#sha256=7a28eb2a9774d00c7bc92411c19a89209d5da7c4c9a9e227be8330a23a25b91f'},
 'multiprocess 0.70.19': {'license': 'BSD-3-Clause',
                          'source': 'https://files.pythonhosted.org/packages/a2/f2/e783ac7f2aeeed14e9e12801f22529cc7e6b7ab80928d6dcce4e9f00922d/multiprocess-0.70.19.tar.gz#sha256=952021e0e6c55a4a9fe4cd787895b86e239a40e76802a789d6305398d3975897'},
 'olefile 0.47': {'license': 'BSD-2-Clause for olefile, with separate Python Imaging Library terms',
                  'notes': ['REVIEW NOTE (optional): The full upstream text includes the separate '
                            'PIL terms. Clarify their OSI classification.'],
                  'source': 'https://files.pythonhosted.org/packages/69/1b/077b508e3e500e1629d366249c3ccb32f95e50258b231705c09e3c7a4366/olefile-0.47.zip#sha256=599383381a0bf3dfbd932ca0ca6515acd174ed48870cbf7fee123d698c192c1c'},
 'omegaconf 2.4.0': {'license': 'BSD-3-Clause',
                     'source': 'https://files.pythonhosted.org/packages/cc/10/1cabf3188a4d9684a06568c7fd76f9536eede625733c8399cf1f5337b83e/omegaconf-2.4.0.tar.gz#sha256=7e219a70bea371da250285b9c01c7db027bc421722799c2cc4ff14bc4a2a753b'},
 'opencv-python-headless 5.1.0.95': {'license': 'MIT for the wheel packaging, Apache-2.0 for '
                                                'OpenCV',
                                     'notes': ['The bundled libavutil reports FFmpeg 9.0.2 and '
                                               'LGPL version 2.1 or later. FFmpeg upstream source: '
                                               'https://github.com/FFmpeg/FFmpeg/tree/946fcce07b6dcd0331c8cc609192aeff5e1924f8',
                                               'The pinned OpenCV source archive contains '
                                               'docker/manylinux_2_28/Dockerfile_x86_64. It '
                                               'downloads the FFmpeg 9.0.2 release tarball and its '
                                               'FFmpeg configure flags match '
                                               'avutil_configuration() in the installed wheel.',
                                               'Corresponding source: FFmpeg 9.0.2, tag n9.0.2 = '
                                               'commit 946fcce07b6dcd0331c8cc609192aeff5e1924f8, '
                                               'https://github.com/FFmpeg/FFmpeg/archive/refs/tags/n9.0.2.tar.gz '
                                               '(sha256 '
                                               '6e374ed621e48faa40639307dff48ba6fe574a509977956d2cce9669b7cc27e9).',
                                               'Build recipe: '
                                               'docker/manylinux_2_28/Dockerfile_x86_64 in the '
                                               'OpenCV sdist above downloads that release tarball '
                                               'and configures FFmpeg with the flags visible in '
                                               'avutil_configuration().',
                                               'FFmpeg ships as separate shared objects '
                                               '(libavcodec, libavformat, libavutil, libswscale, '
                                               'libswresample) in opencv_python_headless.libs/, '
                                               'resolved through $ORIGIN, so a recipient can drop '
                                               'in an interface-compatible build.',
                                               'Corresponding source: GCC 8.5.0-30.el8_10.alma.1 '
                                               'runtime, Alpine-vault source RPM '
                                               'https://repo.almalinux.org/vault/8.10/BaseOS/Source/Packages/gcc-8.5.0-30.el8_10.alma.1.src.rpm '
                                               '(sha256 '
                                               '8b476b1cbfd2acb8096c39baa49f336065bbef0a4f2dbb7e5b0390705ac52f51), '
                                               'as the wheel SBOM records for libgfortran and '
                                               'libquadmath.',
                                               'libgfortran and libquadmath ship as separate '
                                               'shared objects in opencv_python_headless.libs/, '
                                               'resolved through $ORIGIN; libgfortran carries '
                                               'GPL-3.0-or-later WITH GCC-exception-3.1 and '
                                               'libquadmath LGPL-2.1-or-later.'],
                                     'source': 'https://files.pythonhosted.org/packages/6b/51/33b8c7255f3bdd920d6238aa478faf1e2136d5a51f17d826c13902ca7195/opencv_python_headless-5.1.0.95.tar.gz#sha256=9002add14b121b9a0d64dcaff48e5cac69a7987efcf2847f7db075c2d3b9633b'},
 'openpyxl 3.1.5': {'license': 'MIT',
                    'source': 'https://files.pythonhosted.org/packages/3d/f9/88d94a75de065ea32619465d2f77b29a0469500e99012523b91cc4141cd1/openpyxl-3.1.5.tar.gz#sha256=cf0e3cf56142039133628b5acffe8ef0c12bc902d2aadd3e0fe5878dc08d1050'},
 'pgmq 1.1.4': {'files': [{'name': 'pgmq-1.1.4.LICENSE',
                           'sha256': 'c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4',
                           'url': 'https://raw.githubusercontent.com/pgmq/pgmq-py/7e5f139d9b9a1eaf2457b2de3eceda0518159a78/LICENSE'}],
                'license': 'Apache-2.0',
                'source': 'https://raw.githubusercontent.com/pgmq/pgmq-py/7e5f139d9b9a1eaf2457b2de3eceda0518159a78/LICENSE'},
 'pluggy 1.7.0': {'license': 'MIT',
                  'source': 'https://files.pythonhosted.org/packages/bf/db/7fc19e6f2dc92a966727031389fc2e08b558f0f25eb7403c1119ad4713cd/pluggy-1.7.0.tar.gz#sha256=d1eaa46ebb595891b860ab086b4d09c8588af65ebd4361b8e8f4bb8920b90ba8'},
 'polyfactory 3.5.0': {'license': 'MIT',
                       'source': 'https://files.pythonhosted.org/packages/65/19/cc8f17f7778d52a8b930622fc0bb8dd450c1d6d8ed4bdb5b2b7703c85dc4/polyfactory-3.5.0.tar.gz#sha256=78a1e4cf8f317be7da7c111d661f39db35080ec9c12b8df7c01dfbbc1c17250f'},
 'prompt-toolkit 3.0.53': {'license': 'BSD-3-Clause',
                           'source': 'https://files.pythonhosted.org/packages/7d/ea/39b988c938f75cb75d7045b5c69f8bfed47ee2152c8837fb403de29d6fb8/prompt_toolkit-3.0.53.tar.gz#sha256=9ec8a0ad96d5c56148b3f914aa79c1564c3fde5d2e6b876e7bc327e353cf8fa6'},
 'propcache 0.5.4': {'license': 'Apache-2.0',
                     'source': 'https://files.pythonhosted.org/packages/b3/9a/9fbf4e4ec0c2d7f1c32519fff782ef467859b8faa9fbc5331a96f6395d43/propcache-0.5.4.tar.gz#sha256=ff6b113f50bc066a698db5d944d2c6dc7507168dd3341e255a8892fd0715a558'},
 'psutil 7.2.2': {'license': 'BSD-3-Clause',
                  'source': 'https://files.pythonhosted.org/packages/aa/c6/d1ddf4abb55e93cebc4f2ed8b5d6dbad109ecb8d63748dd2b20ab5e57ebe/psutil-7.2.2.tar.gz#sha256=0746f5f8d406af344fd547f1c8daa5f5c33dbc293bb8d6a16d80b4bb88f59372'},
 'psycopg 3.3.9': {'license': 'LGPL-3.0-only',
                   'source': 'https://files.pythonhosted.org/packages/b4/74/ff710399dfa9fe9b546c2f5a05ea2f9b9d1dc2145c0b59e53a3fa2013679/psycopg-3.3.9.tar.gz#sha256=87c1d85c3e0e88e811d3c0b3808f947bd967c6faf34af6cd86fd021f77793d6c'},
 'psycopg-binary 3.3.9': {'files': [{'name': 'psycopg-binary-3.3.9-libpq-18.6.COPYRIGHT',
                                     'sha256': '3d6af92ff8a4c2cdf69afb1cf44edea727922f5cd0cf8b5f72b11cdecac8fdfd',
                                     'url': 'https://raw.githubusercontent.com/postgres/postgres/724edf9bde9d356724ad384a2e196edc3c9f80f7/COPYRIGHT'},
                                    {'name': 'psycopg-binary-3.3.9-openssl-3.5.9.LICENSE',
                                     'sha256': '7d5450cb2d142651b8afa315b5f238efc805dad827d91ba367d8516bc9d49e7a',
                                     'url': 'https://raw.githubusercontent.com/openssl/openssl/45e844fa2a14ec92d146bd8f5778ac130b6625fb/LICENSE.txt'}],
                          'license': 'LGPL-3.0-only for Psycopg',
                          'notes': ['Bundled libpq 18.6 and OpenSSL 3.5.9 were observed. Upstream '
                                    'wheel recipe: '
                                    'https://github.com/psycopg/psycopg/blob/bced8a8993064a378d5ca852215d99f73f4c6cae/.github/workflows/packages-bin.yml',
                                    'Upstream libpq 18.6 COPYRIGHT: '
                                    'https://raw.githubusercontent.com/postgres/postgres/724edf9bde9d356724ad384a2e196edc3c9f80f7/COPYRIGHT',
                                    'Upstream OpenSSL 3.5.9 LICENSE: '
                                    'https://raw.githubusercontent.com/openssl/openssl/45e844fa2a14ec92d146bd8f5778ac130b6625fb/LICENSE.txt',
                                    'Corresponding source: PostgreSQL 18.6, '
                                    'https://ftp.postgresql.org/pub/source/v18.6/postgresql-18.6.tar.bz2 '
                                    '(sha256 '
                                    '555610c24d53e4316da5b7d3fc25c279d96856d5e0e23ee308c328c5fa881d9f), '
                                    'and OpenSSL 3.5.9, '
                                    'https://github.com/openssl/openssl/releases/download/openssl-3.5.9/openssl-3.5.9.tar.gz '
                                    '(sha256 '
                                    '603f5602e2eef00d77fbd429d34dcd5822bb301757a1bc9cdb24c670f1eb859a).',
                                    'Build recipe: '
                                    'https://github.com/psycopg/psycopg/blob/bced8a8993064a378d5ca852215d99f73f4c6cae/.github/workflows/packages-bin.yml '
                                    'builds those two versions into the wheel.',
                                    'libpq and OpenSSL ship as separate shared objects (libpq, '
                                    'libssl, libcrypto, librdkafka-free dependencies in '
                                    'psycopg_binary.libs/), resolved through the extension RPATH '
                                    '$ORIGIN/../psycopg_binary.libs, so a recipient can replace '
                                    'them with an interface-compatible build. The notices for '
                                    'libpq and OpenSSL are included; remaining bundled support '
                                    'libraries (Kerberos, LDAP, SASL, libcrypt) are separate '
                                    'shared objects too, each retaining its own license in the '
                                    'same directory.'],
                          'source': 'https://github.com/psycopg/psycopg/tree/bced8a8993064a378d5ca852215d99f73f4c6cae'},
 'psycopg-pool 3.3.3': {'license': 'LGPL-3.0-only',
                        'source': 'https://files.pythonhosted.org/packages/74/5e/c0664b968b102ff68b811d999c728546c48d5c1eec03e3bbaf88c0cb4472/psycopg_pool-3.3.3.tar.gz#sha256=df87b5d9d0ad7db37f6cdad4fa8ce113d250f5997f6db38e9a99192fb67f9e1d'},
 'pyclipper 1.4.0': {'license': 'MIT',
                     'source': 'https://files.pythonhosted.org/packages/f6/21/3c06205bb407e1f79b73b7b4dfb3950bd9537c4f625a68ab5cc41177f5bc/pyclipper-1.4.0.tar.gz#sha256=9882bd889f27da78add4dd6f881d25697efc740bf840274e749988d25496c8e1'},
 'pylatexenc 2.11': {'license': 'MIT',
                     'source': 'https://files.pythonhosted.org/packages/52/45/ddb0fb04acf95fe9cf9c369814dbdd08651bd2c9ee455f142651e06f4488/pylatexenc-2.11.tar.gz#sha256=305a072a99ce736246049c9da05841b9d718c0f7ea8888f5f596cf15cb621053'},
 'pypdfium2 5.15.2': {'license': 'BSD-3-Clause and Apache-2.0 texts identified for pypdfium2',
                      'notes': ['REVIEW NOTE (optional): The wheel includes PDFium build licenses '
                                'and notices. Check the references against the Linux wheel if the '
                                'artifact changes; no specific missing notice was identified.'],
                      'source': 'https://files.pythonhosted.org/packages/fb/47/8555ff96dd676dd7b2029b8c98a8afd255f5f9eef4a2f4a0445566a323bb/pypdfium2-5.15.2.tar.gz#sha256=0185193bbdf0cc39f0bf31954c25f12edb81048bcb628ea4fc3dccf8d2ce2aef'},
 'python-dateutil 2.9.0.post0': {'license': 'Apache-2.0 for post-2017 and relicensed '
                                            'contributions, with BSD-3-Clause retained for earlier '
                                            'contributions',
                                 'source': 'https://files.pythonhosted.org/packages/66/c0/0c8b6ad9f17a802ee498c46e004a0eb49bc148f2fd230864601a86dcf6db/python-dateutil-2.9.0.post0.tar.gz#sha256=37dd54208da7e1cd875388217d5e00ebd4179249f90fb72437e91a35459a0ad3'},
 'python-docx 1.2.0': {'license': 'MIT',
                       'source': 'https://files.pythonhosted.org/packages/a9/f7/eddfe33871520adab45aaa1a71f0402a2252050c14c7e3009446c8f4701c/python_docx-1.2.0.tar.gz#sha256=7bc9d7b7d8a69c9c02ca09216118c86552704edc23bac179283f2e38f86220ce'},
 'python-dotenv 1.2.4': {'license': 'BSD-3-Clause',
                         'source': 'https://files.pythonhosted.org/packages/74/26/2fbeedb218a787a5eea551c7532cac4e009f83d689dd2faa0d0353473f86/python_dotenv-1.2.4.tar.gz#sha256=f0d53e69935a851c0dcc78f3ab7aaccd8cabef0b92382b576b824212902873c0'},
 'python-oxmsg 0.0.2': {'license': 'MIT',
                        'source': 'https://files.pythonhosted.org/packages/a2/4e/869f34faedbc968796d2c7e9837dede079c9cb9750917356b1f1eda926e9/python_oxmsg-0.0.2.tar.gz#sha256=a6aff4deb1b5975d44d49dab1d9384089ffeec819e19c6940bc7ffbc84775fad'},
 'python-pptx 1.0.2': {'license': 'MIT',
                       'source': 'https://files.pythonhosted.org/packages/52/a9/0c0db8d37b2b8a645666f7fd8accea4c6224e013c42b1d5c17c93590cd06/python_pptx-1.0.2.tar.gz#sha256=479a8af0eaf0f0d76b6f00b0887732874ad2e3188230315290cd1f9dd9cc7095'},
 'pyyaml 6.0.4': {'license': 'MIT',
                  'source': 'https://files.pythonhosted.org/packages/76/b8/c0b780928e2fb1c87e9e375818c6f121da2398b071012d1747be300b83f4/pyyaml-6.0.4.tar.gz#sha256=e24bf71af8a432d7808fabff134d38dfe16d3cbb41f2873bb4b1e42422de0a1a'},
 'requests 2.34.2': {'license': 'Apache-2.0',
                     'source': 'https://files.pythonhosted.org/packages/ac/c3/e2a2b89f2d3e2179abd6d00ebd70bff6273f37fb3e0cc209f48b39d00cbf/requests-2.34.2.tar.gz#sha256=f288924cae4e29463698d6d60bc6a4da69c89185ad1e0bcc4104f584e960b9ed'},
 'requests-toolbelt 1.0.0': {'license': 'Apache-2.0',
                             'source': 'https://files.pythonhosted.org/packages/f3/61/d7545dafb7ac2230c70d38d31cbfe4cc64f7144dc41f6e4e4b78ecd9f5bb/requests-toolbelt-1.0.0.tar.gz#sha256=7681a0a3d047012b5bdc0ee37d7f8f07ebe76ab08caeccfc3921ce23c88d5bc6'},
 'rich 15.0.0': {'license': 'MIT',
                 'source': 'https://files.pythonhosted.org/packages/c0/8f/0722ca900cc807c13a6a0c696dacf35430f72e0ec571c4275d2371fca3e9/rich-15.0.0.tar.gz#sha256=edd07a4824c6b40189fb7ac9bc4c52536e9780fbbfbddf6f1e2502c31b068c36'},
 's3transfer 0.19.3': {'license': 'Apache-2.0',
                       'source': 'https://files.pythonhosted.org/packages/49/c5/0a5ce655109cb96b8dfad36908a4e108d15f5dc2eff8fbc3146c4312e704/s3transfer-0.19.3.tar.gz#sha256=8326acc3d056d7f7b8a37110c7d2aea41f67e83f6ae79cd3b5fbcca34c726406'},
 'safetensors 0.8.0': {'license': 'Apache-2.0 for upstream source',
                       'notes': ['REVIEW NOTE (optional): Compare the Rust runtime dependencies '
                                 'with the retained notices. No specific missing attribution was '
                                 'identified.'],
                       'source': 'https://files.pythonhosted.org/packages/45/06/f955dbbb1859e3bd23c8ac6141af5106e7ad5fedec4a3a6e3d60f94b7001/safetensors-0.8.0.tar.gz#sha256=fabaf3e0f18a6618d9b36560682562157f77c2b71fcffc7b432be2baed9d753d'},
 'scipy 1.18.1': {'license': 'BSD-3-Clause for SciPy',
                  'notes': ['The wheel includes two libquadmath and two libgfortran binaries. Its '
                            'notice identifies LGPL-2.1-or-later and GPL-3.0-or-later WITH '
                            'GCC-exception-3.1, respectively.',
                            'Upstream GCC sources: https://gcc.gnu.org/git/ ; GCC Runtime Library '
                            'Exception: https://www.gnu.org/licenses/gcc-exception-3.1.html',
                            'Corresponding source: GCC 8.5.0-28.el8_10.alma.1 runtime, AlmaLinux '
                            'vault source RPM '
                            'https://repo.almalinux.org/vault/8.10/BaseOS/Source/Packages/gcc-8.5.0-28.el8_10.alma.1.src.rpm '
                            '(sha256 '
                            'c94dbbd2cd6d5d01a5a6822ea18f8ce34942c25a80e17b4682d547d5e652dbd0), as '
                            'the wheel SBOM records for libgfortran and libquadmath.',
                            'libgfortran and libquadmath ship as separate shared objects in '
                            'scipy.libs/, resolved through $ORIGIN, so a recipient can replace '
                            'them with an interface-compatible build.'],
                  'source': 'https://files.pythonhosted.org/packages/7e/74/66de6258867beb2ef08f35f9f2ac017a52cacd5081714d239ff1a442d458/scipy-1.18.1.tar.gz#sha256=52c4b7422442aba924d03ad4019852b08a92e64ea187b933135687bfe2747307'},
 'semchunk 3.2.5': {'license': 'MIT',
                    'source': 'https://files.pythonhosted.org/packages/a9/a0/ce7e3d6cc76498fd594e667d10a03f17d7cced129e46869daec23523bf5a/semchunk-3.2.5.tar.gz#sha256=ee15e9a06a69a411937dd8fcf0a25d7ef389c5195863140436872a02c95b0218'},
 'shellingham 1.5.4': {'license': 'ISC',
                       'source': 'https://files.pythonhosted.org/packages/58/15/8b3609fd3830ef7b27b655beb4b4e9c62313a4e8da8c676e142cc210d58e/shellingham-1.5.4.tar.gz#sha256=8dbca0739d487e5bd35ab3ca4b36e11c4078f3a234bfce294b0a0291363404de'},
 'six 1.17.0': {'license': 'MIT',
                'source': 'https://files.pythonhosted.org/packages/94/e7/b2c673351809dca68a0e064b6af791aa332cf192da575fd474ed7d6f16a2/six-1.17.0.tar.gz#sha256=ff70335d468e7eb6ec65b95b99d3a2836546063f63acc5171de367e834932a81'},
 'sniffio 1.3.1': {'license': 'MIT OR Apache-2.0',
                   'source': 'https://files.pythonhosted.org/packages/a2/87/a6771e1546d97e7e041b6ae58d80074f81b7d5121207425c964ddf5cfdbd/sniffio-1.3.1.tar.gz#sha256=f4324edc670a0f49750a81b895f35c3adb843cca46f0530f79fc1babb23789dc'},
 'sympy 1.14.0': {'license': 'BSD-3-Clause for SymPy and the identified BSD derivatives, with MIT '
                             'for the included latex2sympy code',
                  'source': 'https://files.pythonhosted.org/packages/83/d3/803453b36afefb7c2bb238361cd4ae6125a569b4db67cd9e79846ba2d68c/sympy-1.14.0.tar.gz#sha256=d3d3fe8df1e5a0b42f0e7bdf50541697dbe7d23746e894990c030e2b05e72517'},
 'tiktoken 0.14.0': {'license': 'MIT for upstream source',
                     'notes': ['REVIEW NOTE (optional): Compare the Rust runtime dependencies with '
                               'the retained notices. No specific missing attribution was '
                               'identified.'],
                     'source': 'https://files.pythonhosted.org/packages/66/62/167a842aa0429d45f5e797354fd4343a96f6043d67d0513c675c7b8d36e6/tiktoken-0.14.0.tar.gz#sha256=231dec90efcdccf1b565a1416107736f1e09b1a08fe736ef9d6363e626d03874'},
 'tokenizers 0.23.3': {'files': [{'name': 'tokenizers-0.23.3.LICENSE',
                                  'sha256': 'c71d239df91726fc519c6eb72d318ec65820627232b2f796219e87dcf35d0ab4',
                                  'url': 'https://raw.githubusercontent.com/huggingface/tokenizers/301277072263fb3830eb66eec7e47adadf76f1dc/LICENSE'}],
                       'license': 'Apache-2.0',
                       'notes': ['REVIEW NOTE (optional): Compare the Rust runtime dependencies '
                                 'with the retained notices. No specific missing attribution was '
                                 'identified.'],
                       'source': 'https://raw.githubusercontent.com/huggingface/tokenizers/301277072263fb3830eb66eec7e47adadf76f1dc/LICENSE'},
 'torchvision 0.29.2+cpu': {'files': [{'name': 'torchvision-0.29.2+cpu-libjpeg-3.1.3.LICENSE.md',
                                       'sha256': '2189dc45a8fe96204069f8124caa53a148dfdc193f50f584c6ca7849a6072872',
                                       'url': 'https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/af9c1c268520a29adf98cad5138dafe612b3d318/LICENSE.md'},
                                      {'name': 'torchvision-0.29.2+cpu-libjpeg-3.1.3.README.ijg',
                                       'sha256': '75815e3bf6484201a3c3d17a1bbf10f2e8e3237f84df10a2357ea896db2a81d6',
                                       'url': 'https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/af9c1c268520a29adf98cad5138dafe612b3d318/README.ijg'},
                                      {'name': 'torchvision-0.29.2+cpu-libpng-1.6.56.LICENSE',
                                       'sha256': 'bdb0a645ea18c60507d0368379b1ac5474b92255fcc2d115e07486a7672ba526',
                                       'url': 'https://raw.githubusercontent.com/pnggroup/libpng/d5515b5b8be3901aac04e5bd8bd5c89f287bcd33/LICENSE'},
                                      {'name': 'torchvision-0.29.2+cpu-libwebp-1.6.0.COPYING',
                                       'sha256': '5aec868f669e384a22372a4e8a1a6cd7d44c64cd451f960ca69cc170d1e13acf',
                                       'url': 'https://raw.githubusercontent.com/webmproject/libwebp/4fa21912338357f89e4fd51cf2368325b59e9bd9/COPYING'},
                                      {'name': 'torchvision-0.29.2+cpu-libwebp-1.6.0.PATENTS',
                                       'sha256': 'cc3273e0694ea5896145e0677699b53471b03ea43021ddc50e7923fbb9f5023c',
                                       'url': 'https://raw.githubusercontent.com/webmproject/libwebp/4fa21912338357f89e4fd51cf2368325b59e9bd9/PATENTS'},
                                      {'name': 'torchvision-0.29.2+cpu-libzlib-1.3.2.LICENSE',
                                       'sha256': 'e32ff4e00d9d94930537635291da39e7e612703334bf6fde8c7f1686fe8a45a2',
                                       'url': 'https://raw.githubusercontent.com/madler/zlib/da607da739fa6047df13e66a2af6b8bec7c2a498/LICENSE'}],
                            'license': 'BSD-3-Clause',
                            'notes': ['This software is based in part on the work of the '
                                      'Independent JPEG Group.',
                                      'Bundled libjpeg-turbo 3.1.3, libpng 1.6.56, libwebp 1.6.0 '
                                      '(including SharpYUV), and zlib 1.3.2 notices are reproduced '
                                      'below.',
                                      'Upstream jpeg LICENSE.md: '
                                      'https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/af9c1c268520a29adf98cad5138dafe612b3d318/LICENSE.md',
                                      'Upstream jpeg README.ijg: '
                                      'https://raw.githubusercontent.com/libjpeg-turbo/libjpeg-turbo/af9c1c268520a29adf98cad5138dafe612b3d318/README.ijg',
                                      'Upstream png LICENSE: '
                                      'https://raw.githubusercontent.com/pnggroup/libpng/d5515b5b8be3901aac04e5bd8bd5c89f287bcd33/LICENSE',
                                      'Upstream webp COPYING: '
                                      'https://raw.githubusercontent.com/webmproject/libwebp/4fa21912338357f89e4fd51cf2368325b59e9bd9/COPYING',
                                      'Upstream webp PATENTS: '
                                      'https://raw.githubusercontent.com/webmproject/libwebp/4fa21912338357f89e4fd51cf2368325b59e9bd9/PATENTS',
                                      'Upstream zlib LICENSE: '
                                      'https://raw.githubusercontent.com/madler/zlib/da607da739fa6047df13e66a2af6b8bec7c2a498/LICENSE'],
                            'source': 'https://raw.githubusercontent.com/pytorch/vision/f12281517f1b2f6255904639b027c9436f3e5008/LICENSE'},
 'tqdm 4.70.1': {'license': 'MPL-2.0 AND MIT',
                 'source': 'https://files.pythonhosted.org/packages/0d/ea/b2a5bd54b28a324dae8211928b2d730b6547500342c7e6c6dea08bd0a485/tqdm-4.70.1.tar.gz#sha256=cefd0eca11b2a37a3aee776544d4f4ae913f02688135b5556b8788dfa474afc4'},
 'transformers 5.24.1': {'license': 'Apache-2.0',
                         'source': 'https://files.pythonhosted.org/packages/cb/35/b341c8fddb57f9b404438d048ff0d41d6523b3c6440f8eeed0bb3f4e1725/transformers-5.24.1.tar.gz#sha256=71fae247d9ad61f553fb84de2089f427f5ff2c080686f9deb51e660487e8c270'},
 'tree-sitter 0.26.1': {'license': 'MIT',
                        'source': 'https://files.pythonhosted.org/packages/e6/ca/3d69b21b2328dadee34a6cae65bc84e2b4e0c72bfb9147b7a2d4ba8110a0/tree_sitter-0.26.1.tar.gz#sha256=9919cbaf02d17fa9a4cd0e6da496aecf901b758e6eacf122eee393e3ef08aacd'},
 'tree-sitter-c 0.24.2': {'license': 'MIT',
                          'source': 'https://files.pythonhosted.org/packages/a6/c9/3834f3d9278251aea7312274971bc4c45b17aec2490fd4b884d93bd7019a/tree_sitter_c-0.24.2.tar.gz#sha256=1628584df0299b5a340aa63f8e67b6c97c91517f52fa7e7a4c557e40adb330a9'},
 'tree-sitter-javascript 0.25.0': {'license': 'MIT',
                                   'source': 'https://files.pythonhosted.org/packages/59/e0/e63103c72a9d3dfd89a31e02e660263ad84b7438e5f44ee82e443e65bbde/tree_sitter_javascript-0.25.0.tar.gz#sha256=329b5414874f0588a98f1c291f1b28138286617aa907746ffe55adfdcf963f38'},
 'tree-sitter-python 0.25.0': {'license': 'MIT',
                               'source': 'https://files.pythonhosted.org/packages/b8/8b/c992ff0e768cb6768d5c96234579bf8842b3a633db641455d86dd30d5dac/tree_sitter_python-0.25.0.tar.gz#sha256=b13e090f725f5b9c86aa455a268553c65cadf325471ad5b65cd29cac8a1a68ac'},
 'tree-sitter-typescript 0.23.2': {'license': 'MIT',
                                   'source': 'https://files.pythonhosted.org/packages/1e/fc/bb52958f7e399250aee093751e9373a6311cadbe76b6e0d109b853757f35/tree_sitter_typescript-0.23.2.tar.gz#sha256=7b167b5827c882261cb7a50dfa0fb567975f9b315e87ed87ad0a0a3aedb3834d'},
 'vine 5.1.0': {'license': 'BSD-3-Clause',
                'source': 'https://files.pythonhosted.org/packages/bd/e4/d07b5f29d283596b9727dd5275ccbceb63c44a1a82aa9e4bfd20426762ac/vine-5.1.0.tar.gz#sha256=8b62e981d35c41049211cf62a0a1242d8c1ee9bd15bb196ce38aefd6799e61e0'},
 'wcwidth 0.9.5': {'files': [{'member': 'wcwidth-0.9.5/libwcwidth/LICENSE',
                              'name': 'wcwidth-0.9.5-libwcwidth.LICENSE',
                              'sha256': '70b98a95a2144eb70af8017fa8c6d95ce247e40867436e8bc649e137fe13d21a',
                              'url': 'https://files.pythonhosted.org/packages/8e/da/d59374b2c893ae70a41fdb064637ccef218f25df8c67e5431a96708235d9/wcwidth-0.9.5.tar.gz#sha256=713534e2ecb16e0fc39f19e0a5dda997da76d010953e9e8f803e7971f8accd80'},
                             {'name': 'wcwidth-0.9.5-Unicode.LICENSE',
                              'sha256': 'e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96',
                              'url': 'https://www.unicode.org/license.txt'}],
                   'license': 'MIT for wcwidth, with a separate libwcwidth notice',
                   'notes': ['The bundled C extension uses libwcwidth. The upstream '
                             'libwcwidth/LICENSE from the pinned source archive is reproduced '
                             'below.',
                             'Generated tables include Unicode Character Database inputs. The '
                             'upstream generator and the installed East Asian Width table identify '
                             'the data. Unicode data notice: https://www.unicode.org/license.txt'],
                   'source': 'https://files.pythonhosted.org/packages/8e/da/d59374b2c893ae70a41fdb064637ccef218f25df8c67e5431a96708235d9/wcwidth-0.9.5.tar.gz#sha256=713534e2ecb16e0fc39f19e0a5dda997da76d010953e9e8f803e7971f8accd80'},
 'xxhash 4.1.0': {'license': 'BSD-2-Clause',
                  'source': 'https://files.pythonhosted.org/packages/c9/b5/3d11d3830ace56d67ea83136c24385c3b07da5a9bc62b73b0b94ac096da3/xxhash-4.1.0.tar.gz#sha256=c9b288e4c1f5dae54417bc8a05e6f6daf505783d916b4783f4dd2f2e7d5db486'},
 'yarl 1.25.1': {'license': 'Apache-2.0',
                 'source': 'https://files.pythonhosted.org/packages/75/16/e8be8e2fb175bbf41a0680381a319f1199fae256588241a2ac8677eafb49/yarl-1.25.1.tar.gz#sha256=03dd38de09bc213e9a8b29761eec33ee1d5318dac0e49d8af36e4d27830e23a7'}}

PYTHON_LEGAL = re.compile(r"(?i)^(licen[cs]es?|copying|notice|copyright)([._-].*)?$")

CODE = (".go", ".js", ".mjs", ".cjs", ".ts", ".json", ".py")


def legal_files(directory: pathlib.Path) -> list[pathlib.Path]:
    return sorted(p for p in directory.iterdir()
                  if p.is_file() and LEGAL.match(p.name) and not p.name.endswith(CODE))


def go_modules() -> dict[tuple[str, str], tuple[pathlib.Path, set[str]]]:
    modules: dict[tuple[str, str], tuple[pathlib.Path, set[str]]] = {}
    fmt = "{{with .Module}}{{if not .Main}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}{{end}}"
    for module in ("operator", "capabilities"):
        out = subprocess.run(["go", "list", "-deps", "-f", fmt, "./cmd/..."], cwd=ROOT / module,
                             check=True, capture_output=True, text=True).stdout
        for line in filter(None, out.splitlines()):
            path, version, directory = line.split("\t")
            modules.setdefault((path, version), (pathlib.Path(directory), set()))[1].add(module)
    return modules


def npm_runtime() -> list[tuple[str, str, pathlib.Path]]:
    ui = ROOT / "workspace-ui"
    packages = json.loads((ui / "package-lock.json").read_text())["packages"]
    return sorted((key.removeprefix("node_modules/"), meta["version"], ui / key)
                  for key, meta in packages.items()
                  if key and not meta.get("dev") and not meta.get("devOptional"))


# The Apache-2.0 terms are in the repository's LICENSE; each copy is replaced by a
# pointer, keeping whatever surrounds it (copyright lines, appendix).
APACHE_TERMS = re.compile(r"Apache License\s+Version 2\.0, January 2004.*?END OF TERMS AND CONDITIONS", re.S)


def text(path: pathlib.Path) -> str:
    return APACHE_TERMS.sub("[Apache License, Version 2.0: full terms in LICENSE at the repository root]",
                            path.read_text(errors="replace")).strip()


def section(title: str, files: list, notes: list[str] | None = None) -> str:
    """One section; each file is a path or a (display name, path) pair."""
    if not files and not notes:
        sys.exit(f"no license file for {title}")
    labelled = [(f[0], f[1]) if isinstance(f, tuple) else (f.name, f) for f in files]
    body = "".join(f"{note}\n" for note in notes or [])
    body += "\n".join(f"--- {name} ---\n{text(path)}\n" for name, path in labelled)
    return f"{'=' * 79}\n{title}\n{'=' * 79}\n{body}\n"


def download(url: str) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "opendx-os-notices"})
    with urllib.request.urlopen(request, timeout=120) as response:
        return response.read()


def supplemental(entry: dict) -> list[tuple[str, pathlib.Path]]:
    """Downloads each reviewed text once into CACHE and verifies its SHA-256.

    A text published only inside a release archive names the archive member in
    `member`; the archive is verified and the member is extracted by name.
    """
    paths = []
    for item in entry.get("files", []):
        cached = CACHE / item["sha256"]
        if not cached.is_file():
            CACHE.mkdir(parents=True, exist_ok=True)
            payload = download(item["url"])
            if item.get("member"):
                with tarfile.open(fileobj=io.BytesIO(payload)) as archive:
                    payload = archive.extractfile(item["member"]).read()
            digest = hashlib.sha256(payload).hexdigest()
            if digest != item["sha256"]:
                sys.exit(f"{item['url']}: sha256 {digest} does not match the recorded {item['sha256']}")
            cached.write_bytes(payload)
        elif hashlib.sha256(cached.read_bytes()).hexdigest() != item["sha256"]:
            sys.exit(f"{cached}: cached bytes no longer match {item['sha256']}; delete it and rerun")
        paths.append((item["name"], cached))
    return paths


def python_sections(runtime: list[tuple[str, str]], site: pathlib.Path) -> list[str]:
    def normalize(name: str) -> str:
        return re.sub(r"[-_.]+", "-", name).lower()

    root = site.resolve()
    installed = {(normalize(dist.name), dist.version): dist
                 for dist in importlib.metadata.distributions(path=[str(site)]) if dist.name}
    parts = []
    for name, version in sorted(runtime):
        title = f"{name} {version} (Python; AI image)"
        dist = installed.get((normalize(name), version))
        if dist is None:
            parts.append(section(title, [], ["UNVERIFIED: locked distribution metadata unavailable"]))
            continue

        declared = set(dist.metadata.get_all("License-File", []))
        found = set()
        files = []
        has_license = False
        for entry in sorted(dist.files or []):
            relative = ""
            if entry.parts[0].endswith(".dist-info"):
                relative_parts = entry.parts[1:]
                if relative_parts and relative_parts[0] == "licenses":
                    relative_parts = relative_parts[1:]
                relative = pathlib.PurePath(*relative_parts).as_posix()
            is_legal = PYTHON_LEGAL.match(entry.name) and not entry.name.lower().endswith(CODE)
            if relative not in declared and not is_legal:
                continue
            path = dist.locate_file(entry)
            if not path.is_file() or not path.resolve().is_relative_to(root):
                continue
            if relative in declared:
                found.add(relative)
            files.append(path)
            has_license |= not entry.name.lower().startswith("notice")

        expression = dist.metadata.get("License-Expression")
        evidence = REVIEWED.get(f"{name} {version}")
        notes = [f"Declared license expression: {expression}"] if expression else []
        if evidence:
            notes.extend([f"Reviewed upstream license: {evidence['license']}",
                          f"Upstream license evidence: {evidence['source']}"])
            for item in evidence.get("files", []):
                notes.append(f"Supplemental text {item['name']} ({item['sha256']}): {item['url']}")
            files.extend(supplemental(evidence))
            has_license |= bool(evidence.get("files"))
            notes.extend(evidence.get("notes", []))
        elif not expression:
            notes.append("UNVERIFIED: license expression unverified (no wheel License-Expression)")
        if not has_license:
            notes.append("UNVERIFIED: license text unavailable in installed wheel")
        notes.extend(f"UNVERIFIED: declared file unavailable: {filename}" for filename in sorted(declared - found))
        parts.append(section(title, files, notes))
    return parts


def python_runtime() -> list[str]:
    from packaging.markers import Marker

    lock = subprocess.run(
        ["uv", "export", "--locked", "--no-default-groups", "--no-emit-project",
         "--format", "pylock.toml"],
        cwd=ROOT / "capabilities/ai", check=True, capture_output=True, text=True,
    ).stdout
    runtime = [(pkg["name"], pkg["version"]) for pkg in tomllib.loads(lock)["packages"]
               if "marker" not in pkg or Marker(pkg["marker"]).evaluate()]
    return python_sections(runtime, pathlib.Path(sysconfig.get_path("purelib")))


def render() -> str:
    goroot = pathlib.Path(subprocess.run(["go", "env", "GOROOT"], cwd=ROOT / "operator", check=True,
                                         capture_output=True, text=True).stdout.strip())
    goversion = subprocess.run(["go", "env", "GOVERSION"], cwd=ROOT / "operator", check=True,
                               capture_output=True, text=True).stdout.strip()
    parts = [
        "Third-party license texts for OpenDX-OS builds.\n"
        "Generated by scripts/third_party_notices.py; do not edit by hand.\n"
        "Go modules are linked into the binaries of the named module directory;\n"
        "npm packages are bundled into the Workspace UI. Python distributions are\n"
        "installed in the AI image. See COPYRIGHT.txt.\n\n",
        section(f"Go standard library ({goversion}), linked into every Go binary",
                [goroot / "LICENSE"]),
    ]
    for (path, version), (directory, used_by) in sorted(go_modules().items()):
        parts.append(section(f"{path} {version} (Go; {', '.join(sorted(used_by))})", legal_files(directory)))
    for name, version, directory in npm_runtime():
        parts.append(section(f"{name} {version} (npm; workspace-ui bundle)", legal_files(directory)))
    parts.extend(python_runtime())
    return "".join(parts)


def main() -> int:
    venv = ROOT / "capabilities/ai/.venv"
    if pathlib.Path(sys.prefix).resolve() != venv.resolve():
        interpreter = venv / "bin/python"
        if not interpreter.is_file():
            sys.exit("Prepare capabilities/ai/.venv with a locked uv sync before running this script.")
        os.execv(interpreter, [str(interpreter), str(pathlib.Path(__file__).resolve()), *sys.argv[1:]])
    if (sys.implementation.name, sys.version_info[:2], sys.platform, platform.machine()) != (
        "cpython", (3, 12), "linux", "x86_64"
    ):
        sys.exit("The AI image inventory requires Linux x86-64 CPython 3.12.")
    text = render()
    title = ""
    for line in text.splitlines():
        if line.endswith("(Python; AI image)"):
            title = line
        elif line.startswith("UNVERIFIED:"):
            print(f"{title}: {line}", file=sys.stderr)
    if "--check" in sys.argv[1:]:
        if not OUT.exists() or OUT.read_bytes() != text.encode("utf-8"):
            print(f"{OUT.relative_to(ROOT)} is stale; rerun without --check", file=sys.stderr)
            return 1
        return 0
    OUT.write_bytes(text.encode("utf-8"))
    print(f"wrote {OUT.relative_to(ROOT)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
