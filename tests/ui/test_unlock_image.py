import os
import unittest
import helper
from client import ImageMonkeyWebClient
from webdriver import ImageMonkeyChromeWebDriver


class TestUnlockImage(unittest.TestCase):
	def setUp(self):
		self._driver = ImageMonkeyChromeWebDriver()
		self._client = ImageMonkeyWebClient(self._driver)

	@classmethod
	def setUpClass(cls):
		helper.initialize_with_moderator()

	def tearDown(self):
		self._driver.quit()

	def test_unlock_image_should_succeed(self):
		self._client.donate(os.path.abspath(".." + os.path.sep + "images" + os.path.sep + "apples" + os.path.sep + "apple2.jpeg"), "apple", True)
		self._client.login("moderator", "moderator", True)
		self._client.unlock_image()