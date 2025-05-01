#!/usr/bin/env python
import os
import sys
import json
import yaml
import shutil
import inspect
import logging
import subprocess

from os.path import join, abspath, dirname


class CertBootstrapper:

    def __init__(self):
        self.init_logging()
        self.init_dirs()

    def main(self):
        self.generate_certificate_authority()
        self.generate_service_certs()
        self.generate_local_certs()

    # -----------------------------------------------------------------------
    # Certificate authority
    # -----------------------------------------------------------------------
    def generate_certificate_authority(self):
        if not self.check_certificate_authority():
            self.create_certificate_authority()

    def check_certificate_authority(self):
        # Bail if cert already exists.
        if os.path.isfile(self.ca_cert_path()):
            self.info("CA cert already exists at: %s", self.ca_cert_path())
            return True

    def create_certificate_authority(self):
        # Generate a new CSR.
        self.info("CA Cert not found. Generating one.")
        with open(self.ca_csr_path(), 'w') as f:
            json.dump(self.csr(), f, indent=4)

        # Generate the ca.
        cmd = 'cfssl genkey -initca "{0}" | cfssljson -bare certificate-authority'.format(self.ca_csr_path())
        self.info("Running cfssl command: %s", cmd)
        subprocess.check_call(cmd, shell=True, cwd=self.secret_dir)

        # Create the symlinks.
        os.symlink(self.ca_cert_path(), self.ca_cert_symlink())
        os.symlink(self.ca_key_path(), self.ca_key_symlink())

    # -----------------------------------------------------------------------
    # Service certs
    # -----------------------------------------------------------------------
    def generate_service_certs(self):
        for fn in os.listdir(self.root_dir):
            if not fn.endswith('.yml'):
                continue
            with open(join(self.root_dir, fn)) as f:
                compose = yaml.load(f.read())
            for service in compose['services']:
                # XXX hack
                domainname = service
                if not self.check_service_cert(domainname):
                    self.generate_service_cert(domainname)

    def generate_local_certs(self):
        if not self.check_service_cert('localhost'):
            self.generate_service_cert('localhost')

    def check_service_cert(self, service):
        return os.path.isfile(self.service_cert_path(service))

    def generate_service_cert(self, service):
        csrpath = self.service_csr_path(service)
        secret_path = self.secret_path(service)
        try:
            os.makedirs(secret_path)
        except OSError:
            pass
        # Generate a CSR
        with open(csrpath, 'w') as f:
            json.dump(self.csr(service), f, indent=4)

        # Generte the cert.
        cmd = 'cfssl gencert -ca "{cacert}" -ca-key "{cakey}" "{csrpath}" | cfssljson -bare "{service}"'
        ctx = dict(
                cacert=self.ca_cert_path(),
                cakey=self.ca_key_path(),
                csrpath=csrpath,
                service=service)
        cmd = cmd.format(**ctx)
        self.info("Running cfssl command: %s", cmd)
        subprocess.check_call(cmd, shell=True, cwd=secret_path)
        subprocess.check_call('mv %s.pem cert.pem' % service, shell=True, cwd=secret_path)
        subprocess.check_call('mv %s-key.pem key.pem' % service, shell=True, cwd=secret_path)
        shutil.copy(self.ca_cert_path(), join(self.secret_path(service), 'ca.pem'))
        subprocess.check_call('cat cert.pem key.pem > combined.pem', shell=True, cwd=secret_path)
        subprocess.check_call('cat cert.pem ca.pem > bundle.pem', shell=True, cwd=secret_path)

    def init_dirs(self):
        # Create secret dir if not exists.
        for directory in self.secret_dir, self.etc_dir:
            if not os.path.isdir(directory):
                self.info("Creating directory: %s", directory)
                os.mkdir(directory)

    def init_logging(self):
        logger = logging.getLogger('certs')
        logger.setLevel(logging.DEBUG)
        ch = logging.StreamHandler(sys.stdout)
        ch.setLevel(logging.DEBUG)
        formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')
        ch.setFormatter(formatter)
        logger.addHandler(ch)
        self.logger = logger

        self.info = logger.info
        self.debug = logger.debug
        self.error = logger.error
        self.exception = logger.exception

    @property
    def root_dir(self):
        return dirname(dirname(abspath(__file__)))
 
    @property
    def secret_dir(self):
       return join(self.root_dir, 'run')

    @property
    def etc_dir(self):
        return join(self.root_dir, 'etc')

    def secret_path(self, service):
        return join(self.secret_dir, service)

    def etc_path(self, service):
        return join(self.etc_dir, service)

    def ca_csr_path(self):
        return join(self.secret_dir, "certificate-authority-csr.json")

    def ca_cert_path(self):
        return join(self.secret_dir, "certificate-authority.pem")

    def ca_key_path(self):
        return join(self.secret_dir, "certificate-authority-key.pem")

    def ca_cert_symlink(self):
        return join(self.secret_dir, "ca.pem")

    def ca_key_symlink(self):
        return join(self.secret_dir, "ca-key.pem")

    def service_cert_path(self, service):
        return join(self.secret_path(service), 'cert.pem')

    def service_key_path(self, service):
        return join(self.secret_path(service), 'key.pem')
 
    def service_csr_path(self, service):
        return join(self.secret_path(service), 'csr.json')


    def csr(self, host=None):
        csr = {
            "key": {
                "algo": "rsa",
                "size": 2048
            },
            "names": [{
                "C":  "US",
                "L":  "Boston",
                "O":  "NBIS",
                "OU": "eapp",
                "ST": "Massachussetts"
            }]
        }
        if host is not None:
            csr["hosts"] = [host, "localhost"]
            csr["CN"] = host
        return csr
            

if __name__ == "__main__":
    CertBootstrapper().main()
