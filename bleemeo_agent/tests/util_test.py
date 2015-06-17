
import bleemeo_agent.util


def test_package_isntalled():
    # assumption: bash is installed. Any better package to test?
    assert bleemeo_agent.util.package_installed('bash')

    assert not bleemeo_agent.util.package_installed('not-existing-pacakge')
