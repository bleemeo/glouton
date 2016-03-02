Bleemeo Agent
=============

Bleemeo Agent have been designed to be a central piece of
a monitoring infrastructure. It gather all information and
send it to... something. We provide drivers for storing in
an InfluxDB database directly or a secure connection (MQTT over SSL) to
Bleemeo Cloud platform.


Quickstart
----------

If you want to try bleemeo-agent, here are the step to run from a git checkout:

* create a virtualenv::

    mkvirtualenv -p /usr/bin/python3 bleemeo-agent

* Install bleemeo-agent with its dependencies::

    pip install -r requirements.txt

* Install telegraf and configure it::

    sudo sh -c "echo deb http://packages.bleemeo.com/telegraf/ $(lsb_release --codename | cut -f2) main > /etc/apt/sources.list.d/bleemeo-telegraf.list"
    sudo apt-key adv --keyserver hkp://keyserver.ubuntu.com:80 --recv-keys 9B8BDA4BE10E9F2328D40077E848FD17FC23F27E
    sudo apt-get update

    sudo apt-get install telegraf
    sudo install -m 0644 debian/bleemeo-agent-telegraf.telegraf.conf /etc/telegraf/telegraf.d/bleemeo.conf
    sudo service telegraf restart

* Run bleemeo-agent from repository root (where README and setup.py are)::

    bleemeo-agent

* Bleemeo agent have a local web UI accessible on http://localhost:8015
