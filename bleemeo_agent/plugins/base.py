
import abc

import six


@six.add_metaclass(abc.ABCMeta)
class PluginV1Base(object):
    """ Base class for plugins version 1
    """

    def __init__(self, agent):
        self.agent = agent

    @abc.abstractmethod
    def dependencies_present(self):
        """ Check for dependencies and return True if available
        """

    @abc.abstractmethod
    def collectd_configure(self):
        """ Do action needed to configure collectd

            Return string with section to add in collectd.conf
        """

    @abc.abstractmethod
    def canonical_metric_name(self, name):
        """ Return the canonical name for given metric
        """

    @abc.abstractmethod
    def list_checks(self):
        """ Return list of checks to run.

            The list contains 3-tuple with (name, check_command, tcp_port),
            where:

            * name describe what is checked
            * check_command point to a programm (and it's argument). The
              pointed programm should behave like a nagios check.
            * tcp_port is the TCP port associated with this check. It could
              be None.
        """