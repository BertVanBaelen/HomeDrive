package accounts

import (
	"fmt"

	hdmail "github.com/BertVanBaelen/HomeDrive/server/internal/mail"
)

// codeMessage is the email with a sign-in code, in the user's language.
func codeMessage(to, locale, code string) hdmail.Message {
	pretty := code[:4] + " " + code[4:]
	if locale == "nl" {
		return hdmail.Message{
			To:      to,
			Subject: fmt.Sprintf("Je Homedrive-code: %s", pretty),
			Body: fmt.Sprintf("Je code om aan te melden bij Homedrive is:\n\n    %s\n\n"+
				"De code is 10 minuten geldig.\n\n"+
				"Heb je niet geprobeerd aan te melden? Dan kent iemand je wachtwoord. "+
				"Vraag je beheerder om het te wijzigen.\n", pretty),
		}
	}
	return hdmail.Message{
		To:      to,
		Subject: fmt.Sprintf("Your Homedrive code: %s", pretty),
		Body: fmt.Sprintf("Your code to sign in to Homedrive is:\n\n    %s\n\n"+
			"It is valid for 10 minutes.\n\n"+
			"Didn't try to sign in? Then someone knows your password. "+
			"Ask your administrator to change it.\n", pretty),
	}
}

// linkMessage is the email with a link to choose a password: an
// invitation for a new account, or a reset made by an admin. An account
// that logs in with Home Assistant only chooses its first password with a
// reset link, which it may also ask for itself.
func linkMessage(u User, adminName, link, purpose string) hdmail.Message {
	if purpose == "reset" && u.HAOnly {
		if u.Locale == "nl" {
			return hdmail.Message{
				To:      u.Email,
				Subject: "Kies een wachtwoord voor Homedrive",
				Body: fmt.Sprintf("Hallo %s,\n\nJe meldt je bij Homedrive aan met Home Assistant. "+
					"Met deze link kies je ook een Homedrive-wachtwoord, om je ook met je e-mailadres aan te melden:\n\n%s\n\n"+
					"De link is 24 uur geldig en werkt één keer. Aanmelden met Home Assistant blijft werken.\n", u.Name, link),
			}
		}
		return hdmail.Message{
			To:      u.Email,
			Subject: "Choose a password for Homedrive",
			Body: fmt.Sprintf("Hi %s,\n\nYou log in to Homedrive with Home Assistant. "+
				"With this link you can also choose a Homedrive password, to log in with your email address as well:\n\n%s\n\n"+
				"The link is valid for 24 hours and works once. Logging in with Home Assistant keeps working.\n", u.Name, link),
		}
	}
	if u.Locale == "nl" {
		if purpose == "invite" {
			return hdmail.Message{
				To:      u.Email,
				Subject: fmt.Sprintf("%s nodigt je uit voor Homedrive", adminName),
				Body: fmt.Sprintf("Hallo %s,\n\n%s maakte een Homedrive-account voor je. "+
					"Kies je wachtwoord via deze link:\n\n%s\n\nDe link is 7 dagen geldig en werkt één keer.\n", u.Name, adminName, link),
			}
		}
		return hdmail.Message{
			To:      u.Email,
			Subject: "Kies een nieuw wachtwoord voor Homedrive",
			Body: fmt.Sprintf("Hallo %s,\n\n%s vroeg een nieuw wachtwoord voor je Homedrive-account aan. "+
				"Kies het via deze link:\n\n%s\n\nDe link is 24 uur geldig en werkt één keer. "+
				"Tot dan blijft je oude wachtwoord werken.\n", u.Name, adminName, link),
		}
	}
	if purpose == "invite" {
		return hdmail.Message{
			To:      u.Email,
			Subject: fmt.Sprintf("%s invited you to Homedrive", adminName),
			Body: fmt.Sprintf("Hi %s,\n\n%s created a Homedrive account for you. "+
				"Choose your password with this link:\n\n%s\n\nThe link is valid for 7 days and works once.\n", u.Name, adminName, link),
		}
	}
	return hdmail.Message{
		To:      u.Email,
		Subject: "Choose a new password for Homedrive",
		Body: fmt.Sprintf("Hi %s,\n\n%s asked for a new password for your Homedrive account. "+
			"Choose it with this link:\n\n%s\n\nThe link is valid for 24 hours and works once. "+
			"Until then your old password keeps working.\n", u.Name, adminName, link),
	}
}
